package network

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestSessionSendReceive(t *testing.T) {
	leftConnection, rightConnection := net.Pipe()
	left := mustSession(t, leftConnection, 1024)
	right := mustSession(t, rightConnection, 1024)
	defer left.Close()
	defer right.Close()

	want := Message{Type: MessagePing, RequestID: 7, Payload: []byte("ping")}
	sendErrors := make(chan error, 1)
	go func() {
		sendErrors <- left.Send(context.Background(), want)
	}()

	got, err := right.Receive(context.Background())
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if got.Type != want.Type || got.RequestID != want.RequestID || string(got.Payload) != "ping" {
		t.Fatalf("Receive() = %+v, want %+v", got, want)
	}
	if err := <-sendErrors; err != nil {
		t.Fatalf("Send() error = %v", err)
	}
}

func TestSessionReceiveCanBeCanceled(t *testing.T) {
	leftConnection, rightConnection := net.Pipe()
	left := mustSession(t, leftConnection, 1024)
	defer left.Close()
	defer rightConnection.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := left.Receive(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Receive() error = %v, want context.Canceled", err)
	}
}

func TestSessionReceiveDeadline(t *testing.T) {
	leftConnection, rightConnection := net.Pipe()
	left := mustSession(t, leftConnection, 1024)
	defer left.Close()
	defer rightConnection.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := left.Receive(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Receive() error = %v, want context.DeadlineExceeded", err)
	}
}

func TestSessionCanReceiveAfterCanceledOperation(t *testing.T) {
	leftConnection, rightConnection := net.Pipe()
	left := mustSession(t, leftConnection, 1024)
	right := mustSession(t, rightConnection, 1024)
	defer left.Close()
	defer right.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := left.Receive(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("first Receive() error = %v, want context.Canceled", err)
	}

	sendErrors := make(chan error, 1)
	go func() {
		sendErrors <- right.Send(context.Background(), Message{Type: MessagePing})
	}()
	if _, err := left.Receive(context.Background()); err != nil {
		t.Fatalf("second Receive() error = %v", err)
	}
	if err := <-sendErrors; err != nil {
		t.Fatalf("Send() error = %v", err)
	}
}

func mustSession(t *testing.T, connection net.Conn, maxPayload uint32) Session {
	t.Helper()
	session, err := NewSession(connection, maxPayload)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	return session
}
