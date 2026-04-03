package server

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"

	"barrage/internal/protocol/ws"
)

// WebSocketFrame 表示一个解析后的 WebSocket 帧
type WebSocketFrame struct {
	// 操作码
	Opcode byte

	// 负载内容
	Payload []byte
}

var (
	// ErrIncompleteFrame 表示当前数据不足以解析完整帧
	ErrIncompleteFrame = errors.New("websocket frame not complete")

	// ErrInvalidFrame 表示非法帧
	ErrInvalidFrame = errors.New("invalid websocket frame")

	// ErrNotWebSocket 表示不是合法 websocket 握手请求
	ErrNotWebSocket = errors.New("not websocket handshake request")
)

// BuildWebSocketHandshakeResponse 构造 websocket 握手响应
func BuildWebSocketHandshakeResponse(req []byte) ([]byte, error) {
	key, err := extractWebSocketKey(req)
	if err != nil {
		return nil, err
	}

	acceptKey := makeWebSocketAcceptKey(key)

	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + acceptKey + "\r\n" +
		"\r\n"

	return []byte(resp), nil
}

// extractWebSocketKey 从 HTTP Upgrade 请求中提取 Sec-WebSocket-Key
func extractWebSocketKey(req []byte) (string, error) {
	text := string(req)
	lines := strings.Split(text, "\r\n")

	var hasUpgrade bool
	var hasConnectionUpgrade bool
	var key string

	for _, line := range lines {
		lowerLine := strings.ToLower(line)

		if strings.HasPrefix(lowerLine, "upgrade:") && strings.Contains(lowerLine, "websocket") {
			hasUpgrade = true
		}

		if strings.HasPrefix(lowerLine, "connection:") && strings.Contains(lowerLine, "upgrade") {
			hasConnectionUpgrade = true
		}

		if strings.HasPrefix(lowerLine, "sec-websocket-key:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				key = strings.TrimSpace(parts[1])
			}
		}
	}

	if !hasUpgrade || !hasConnectionUpgrade || key == "" {
		return "", ErrNotWebSocket
	}

	return key, nil
}

// makeWebSocketAcceptKey 生成握手响应中的 Sec-WebSocket-Accept
func makeWebSocketAcceptKey(key string) string {
	const magic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

	h := sha1.New()
	_, _ = h.Write([]byte(key + magic))
	sum := h.Sum(nil)

	return base64.StdEncoding.EncodeToString(sum)
}

// DecodeWebSocketFrames 解析一个或多个 websocket 帧
// 返回值说明：
// 1. frames 表示已经成功解析出的完整帧
// 2. consumed 表示本次一共消费了多少字节
func DecodeWebSocketFrames(data []byte) ([]WebSocketFrame, int, error) {
	var frames []WebSocketFrame
	offset := 0

	for offset < len(data) {
		frame, n, err := decodeOneFrame(data[offset:])
		if err != nil {
			if errors.Is(err, ErrIncompleteFrame) {
				break
			}
			return nil, offset, err
		}

		frames = append(frames, frame)
		offset += n
	}

	return frames, offset, nil
}

// decodeOneFrame 解析单个 websocket 帧
func decodeOneFrame(data []byte) (WebSocketFrame, int, error) {
	if len(data) < 2 {
		return WebSocketFrame{}, 0, ErrIncompleteFrame
	}

	b0 := data[0]
	b1 := data[1]

	fin := (b0 & 0x80) != 0
	opcode := b0 & 0x0F
	masked := (b1 & 0x80) != 0
	payloadLen := int(b1 & 0x7F)

	// 当前简单框架先要求客户端发送完整帧，不处理分片帧
	if !fin {
		return WebSocketFrame{}, 0, ErrInvalidFrame
	}

	// 客户端发给服务端的帧必须带 mask
	if !masked {
		return WebSocketFrame{}, 0, ErrInvalidFrame
	}

	offset := 2

	switch payloadLen {
	case 126:
		if len(data) < offset+2 {
			return WebSocketFrame{}, 0, ErrIncompleteFrame
		}
		payloadLen = int(binary.BigEndian.Uint16(data[offset : offset+2]))
		offset += 2
	case 127:
		if len(data) < offset+8 {
			return WebSocketFrame{}, 0, ErrIncompleteFrame
		}
		v := binary.BigEndian.Uint64(data[offset : offset+8])
		if v > (1<<31 - 1) {
			return WebSocketFrame{}, 0, ErrInvalidFrame
		}
		payloadLen = int(v)
		offset += 8
	}

	if len(data) < offset+4 {
		return WebSocketFrame{}, 0, ErrIncompleteFrame
	}

	maskKey := data[offset : offset+4]
	offset += 4

	if len(data) < offset+payloadLen {
		return WebSocketFrame{}, 0, ErrIncompleteFrame
	}

	payload := make([]byte, payloadLen)
	copy(payload, data[offset:offset+payloadLen])

	for i := 0; i < payloadLen; i++ {
		payload[i] ^= maskKey[i%4]
	}

	offset += payloadLen

	return WebSocketFrame{
		Opcode:  opcode,
		Payload: payload,
	}, offset, nil
}

// BuildPongFrame 构造 Pong 帧
func BuildPongFrame(payload []byte) []byte {
	return ws.BuildPongFrame(payload)
}

// BuildCloseFrame 构造 Close 帧
func BuildCloseFrame(payload []byte) []byte {
	return ws.BuildCloseFrame(payload)
}
