package relay

import (
	"bytes"
	"reflect"
	"testing"
)

func TestRegistrationRoundTrip(t *testing.T) {
	token := bytes.Repeat([]byte{0x42}, tokenSize)
	var buffer bytes.Buffer
	if err := writeRegistration(&buffer, "session", roleSource, token); err != nil {
		t.Fatalf("writeRegistration() error = %v", err)
	}
	got, err := readRegistration(&buffer)
	if err != nil {
		t.Fatalf("readRegistration() error = %v", err)
	}
	want := registration{
		sessionID: "session",
		role:      roleSource,
		tokenHash: got.tokenHash,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("readRegistration() = %+v, want %+v", got, want)
	}
}

func TestRegistrationRejectsUnsafeInput(t *testing.T) {
	if err := writeRegistration(&bytes.Buffer{}, "../session", roleSource, make([]byte, tokenSize)); err == nil {
		t.Fatal("writeRegistration() accepted unsafe session ID")
	}
	if err := writeRegistration(&bytes.Buffer{}, "session", roleSource, []byte("short")); err == nil {
		t.Fatal("writeRegistration() accepted short token")
	}
}
