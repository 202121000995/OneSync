package network

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	ProtocolVersion   uint8  = 1
	DefaultMaxPayload uint32 = 16 << 20
	frameHeaderSize          = 14
)

// MessageType identifies a protocol message.
type MessageType uint8

const (
	MessageHello MessageType = iota + 1
	MessageAuthenticate
	MessageSnapshotRequest
	MessageSnapshotResponse
	MessageSyncPlan
	MessageFileBegin
	MessageFileChunk
	MessageFileEnd
	MessageSyncComplete
	MessageAck
	MessageError
	MessagePing
	MessagePong
)

// Message is one length-delimited protocol frame.
type Message struct {
	Type      MessageType
	RequestID uint64
	Payload   []byte
}

// Codec reads and writes bounded protocol frames.
type Codec struct {
	maxPayload uint32
}

// NewCodec returns a protocol codec with a payload limit.
func NewCodec(maxPayload uint32) (*Codec, error) {
	if maxPayload == 0 || maxPayload > DefaultMaxPayload {
		return nil, fmt.Errorf("max payload must be between 1 and %d bytes", DefaultMaxPayload)
	}
	return &Codec{maxPayload: maxPayload}, nil
}

// Write writes one complete message.
func (c *Codec) Write(writer io.Writer, message Message) error {
	if !validMessageType(message.Type) {
		return fmt.Errorf("invalid message type %d", message.Type)
	}
	if uint64(len(message.Payload)) > uint64(c.maxPayload) {
		return fmt.Errorf("payload length %d exceeds limit %d", len(message.Payload), c.maxPayload)
	}

	header := make([]byte, frameHeaderSize)
	header[0] = ProtocolVersion
	header[1] = byte(message.Type)
	binary.BigEndian.PutUint64(header[2:10], message.RequestID)
	binary.BigEndian.PutUint32(header[10:14], uint32(len(message.Payload)))

	if err := writeFull(writer, header); err != nil {
		return fmt.Errorf("write frame header: %w", err)
	}
	if err := writeFull(writer, message.Payload); err != nil {
		return fmt.Errorf("write frame payload: %w", err)
	}
	return nil
}

// Read reads one complete message.
func (c *Codec) Read(reader io.Reader) (Message, error) {
	header := make([]byte, frameHeaderSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		return Message{}, fmt.Errorf("read frame header: %w", err)
	}
	if header[0] != ProtocolVersion {
		return Message{}, fmt.Errorf("unsupported protocol version %d", header[0])
	}

	messageType := MessageType(header[1])
	if !validMessageType(messageType) {
		return Message{}, fmt.Errorf("invalid message type %d", messageType)
	}

	payloadLength := binary.BigEndian.Uint32(header[10:14])
	if payloadLength > c.maxPayload {
		return Message{}, fmt.Errorf("payload length %d exceeds limit %d", payloadLength, c.maxPayload)
	}

	payload := make([]byte, payloadLength)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return Message{}, fmt.Errorf("read frame payload: %w", err)
	}
	return Message{
		Type:      messageType,
		RequestID: binary.BigEndian.Uint64(header[2:10]),
		Payload:   payload,
	}, nil
}

func writeFull(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if written > 0 {
			data = data[written:]
		}
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func validMessageType(messageType MessageType) bool {
	return messageType >= MessageHello && messageType <= MessagePong
}

var errAuthenticationFailed = errors.New("authentication failed")
