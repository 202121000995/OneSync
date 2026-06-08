package transfer

import (
	"reflect"
	"testing"
)

func TestBeginRoundTrip(t *testing.T) {
	want := fileBegin{Path: "nested/file.txt", Size: 123}
	want.Hash[0] = 1
	want.FileID[0] = 2

	payload, err := encodeBegin(want)
	if err != nil {
		t.Fatalf("encodeBegin() error = %v", err)
	}
	got, err := decodeBegin(payload)
	if err != nil {
		t.Fatalf("decodeBegin() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decodeBegin() = %+v, want %+v", got, want)
	}
}

func TestProtocolRejectsMalformedPayloads(t *testing.T) {
	if _, err := decodeBegin([]byte{0}); err == nil {
		t.Fatal("decodeBegin() accepted truncated payload")
	}
	if _, err := decodeOffset([]byte{0}); err == nil {
		t.Fatal("decodeOffset() accepted wrong length")
	}
	if _, _, err := decodeEnd([]byte{0}); err == nil {
		t.Fatal("decodeEnd() accepted wrong length")
	}
	if _, err := encodeChunk(0, nil); err == nil {
		t.Fatal("encodeChunk() accepted empty chunk")
	}
}
