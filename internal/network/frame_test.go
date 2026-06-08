package network

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"reflect"
	"testing"
)

func TestCodecRoundTrip(t *testing.T) {
	codec := mustCodec(t, 1024)
	want := Message{
		Type:      MessageFileChunk,
		RequestID: 42,
		Payload:   []byte("payload"),
	}

	var buffer bytes.Buffer
	if err := codec.Write(&buffer, want); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	got, err := codec.Read(&buffer)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Read() = %+v, want %+v", got, want)
	}
}

func TestCodecRejectsOversizedPayloadBeforeAllocation(t *testing.T) {
	codec := mustCodec(t, 8)
	header := make([]byte, frameHeaderSize)
	header[0] = ProtocolVersion
	header[1] = byte(MessageFileChunk)
	binary.BigEndian.PutUint32(header[10:14], 9)

	_, err := codec.Read(bytes.NewReader(header))
	if err == nil {
		t.Fatal("Read() error = nil, want payload limit error")
	}
}

func TestCodecRejectsInvalidVersionAndMessageType(t *testing.T) {
	codec := mustCodec(t, 8)

	tests := [][]byte{
		func() []byte {
			header := make([]byte, frameHeaderSize)
			header[0] = ProtocolVersion + 1
			header[1] = byte(MessagePing)
			return header
		}(),
		func() []byte {
			header := make([]byte, frameHeaderSize)
			header[0] = ProtocolVersion
			header[1] = 255
			return header
		}(),
	}

	for _, header := range tests {
		if _, err := codec.Read(bytes.NewReader(header)); err == nil {
			t.Fatalf("Read() accepted invalid header: %v", header)
		}
	}
}

func TestCodecHandlesShortWrites(t *testing.T) {
	codec := mustCodec(t, 1024)
	writer := &limitedWriter{limit: 2}

	if err := codec.Write(writer, Message{
		Type:    MessagePing,
		Payload: []byte("hello"),
	}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	message, err := codec.Read(bytes.NewReader(writer.data.Bytes()))
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if string(message.Payload) != "hello" {
		t.Fatalf("Read() payload = %q, want hello", message.Payload)
	}
}

func TestCodecRejectsZeroProgressWriter(t *testing.T) {
	codec := mustCodec(t, 8)
	err := codec.Write(zeroWriter{}, Message{Type: MessagePing})
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("Write() error = %v, want io.ErrShortWrite", err)
	}
}

func mustCodec(t *testing.T, maxPayload uint32) *Codec {
	t.Helper()
	codec, err := NewCodec(maxPayload)
	if err != nil {
		t.Fatalf("NewCodec() error = %v", err)
	}
	return codec
}

type limitedWriter struct {
	limit int
	data  bytes.Buffer
}

func (w *limitedWriter) Write(data []byte) (int, error) {
	if len(data) > w.limit {
		data = data[:w.limit]
	}
	return w.data.Write(data)
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) {
	return 0, nil
}
