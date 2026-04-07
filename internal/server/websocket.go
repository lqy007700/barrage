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

	// ErrFragmentNotSupported 明确表示不支持数据帧分片
	ErrFragmentNotSupported = errors.New("fragmented frame is not supported")

	// ErrControlFrameInvalid 表示非法的控制帧
	ErrControlFrameInvalid = errors.New("invalid control frame")

	// ErrRSVNotZero 表示 RSV 保留位不符合规范（未经扩展支持）
	ErrRSVNotZero = errors.New("RSV bits must be 0")

	// ErrPayloadTooLarge 表示消息体积过大
	ErrPayloadTooLarge = errors.New("websocket payload too large")
)

const (
	// MaxWebSocketPayloadSize 允许的最大单帧数据载荷，设定为 64KB 防止大包攻击
	MaxWebSocketPayloadSize = 64 * 1024
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
	rsv1 := (b0 & 0x40) != 0
	rsv2 := (b0 & 0x20) != 0
	rsv3 := (b0 & 0x10) != 0
	opcode := b0 & 0x0F
	masked := (b1 & 0x80) != 0
	payloadLen := int(b1 & 0x7F)

	// 严格 header 校验：未协商扩展的情况下 RSV 必须为 0
	if rsv1 || rsv2 || rsv3 {
		return WebSocketFrame{}, 0, ErrRSVNotZero
	}

	// 控制帧要求：不能被分片（FIN 必须为1），并且有效载荷 <= 125 字节
	if opcode >= 0x08 {
		if !fin {
			return WebSocketFrame{}, 0, ErrControlFrameInvalid
		}
		if payloadLen > 125 {
			return WebSocketFrame{}, 0, ErrControlFrameInvalid
		}
	}

	// 数据帧分片支持策略：当前框架设计为单帧全透传直发，显式拒绝 Continuation Frame（分拆包）
	if !fin || opcode == 0x00 {
		return WebSocketFrame{}, 0, ErrFragmentNotSupported
	}

	// 检查业务范围之外的其它保留/异常 Opcode
	if opcode != 0x1 && opcode != 0x2 && opcode != 0x8 && opcode != 0x9 && opcode != 0xA {
		return WebSocketFrame{}, 0, ErrInvalidFrame
	}

	// 客户端发给服务端的帧必须带 mask 掩码
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

	// 拦截超大非法请求保护服务器内存，防止恶意攻击
	if payloadLen > MaxWebSocketPayloadSize {
		return WebSocketFrame{}, 0, ErrPayloadTooLarge
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
