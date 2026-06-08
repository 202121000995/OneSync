package network

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
)

func TestAuthenticationSucceeds(t *testing.T) {
	client, server := sessionPair(t)
	token := bytes.Repeat([]byte{0x42}, minimumTokenLength)

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- AuthenticateServer(context.Background(), server, token)
	}()

	if err := AuthenticateClient(context.Background(), client, 99, token); err != nil {
		t.Fatalf("AuthenticateClient() error = %v", err)
	}
	if err := <-serverErrors; err != nil {
		t.Fatalf("AuthenticateServer() error = %v", err)
	}
}

func TestAuthenticationRejectsWrongToken(t *testing.T) {
	client, server := sessionPair(t)
	expected := bytes.Repeat([]byte{0x42}, minimumTokenLength)
	actual := bytes.Repeat([]byte{0x24}, minimumTokenLength)

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- AuthenticateServer(context.Background(), server, expected)
	}()

	if err := AuthenticateClient(context.Background(), client, 99, actual); !errors.Is(err, errAuthenticationFailed) {
		t.Fatalf("AuthenticateClient() error = %v, want authentication failure", err)
	}
	if err := <-serverErrors; !errors.Is(err, errAuthenticationFailed) {
		t.Fatalf("AuthenticateServer() error = %v, want authentication failure", err)
	}
}

func TestAuthenticationRejectsShortTokens(t *testing.T) {
	if err := validateToken([]byte("short")); err == nil {
		t.Fatal("validateToken() error = nil, want minimum length error")
	}
}

func sessionPair(t *testing.T) (Session, Session) {
	t.Helper()
	clientConnection, serverConnection := net.Pipe()
	client := mustSession(t, clientConnection, 1024)
	server := mustSession(t, serverConnection, 1024)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	return client, server
}
