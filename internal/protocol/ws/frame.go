package ws

import "encoding/binary"

// 协议常量定义
const (
	// 文本帧
	OpText = 0x1

	// 二进制帧
	OpBinary = 0x2

	// 关闭帧
	OpClose = 0x8

	// Ping 帧
	OpPing = 0x9

	// Pong 帧
	OpPong = 0xA
)

// BuildBinaryFrame 构造服务端发送的二进制帧
func BuildBinaryFrame(payload []byte) []byte {
	return buildServerFrame(OpBinary, payload)
}

// BuildPongFrame 构造 Pong 帧
func BuildPongFrame(payload []byte) []byte {
	return buildServerFrame(OpPong, payload)
}

// BuildCloseFrame 构造 Close 帧
func BuildCloseFrame(payload []byte) []byte {
	return buildServerFrame(OpClose, payload)
}

// buildServerFrame 构造服务端 websocket 帧
// 服务端发送给客户端的数据不需要 mask
func buildServerFrame(opcode byte, payload []byte) []byte {
	payloadLen := len(payload)

	var header []byte
	switch {
	case payloadLen < 126:
		header = []byte{
			0x80 | opcode,
			byte(payloadLen),
		}
	case payloadLen <= 65535:
		header = make([]byte, 4)
		header[0] = 0x80 | opcode
		header[1] = 126
		binary.BigEndian.PutUint16(header[2:], uint16(payloadLen))
	default:
		header = make([]byte, 10)
		header[0] = 0x80 | opcode
		header[1] = 127
		binary.BigEndian.PutUint64(header[2:], uint64(payloadLen))
	}

	out := make([]byte, 0, len(header)+payloadLen)
	out = append(out, header...)
	out = append(out, payload...)
	return out
}
