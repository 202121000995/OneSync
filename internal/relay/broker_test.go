package relay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestBrokerRelaysBytesInBothDirections(t *testing.T) {
	broker := mustBroker(t, Config{
		WaitTimeout: time.Second,
		IdleTimeout: time.Second,
		MaxWaiting:  10,
		MaxBytes:    1024,
	})
	token := bytes.Repeat([]byte{0x42}, tokenSize)
	source, target, errorsChannel := connectPair(t, broker, token, token)

	if _, err := source.Write([]byte("source-to-target")); err != nil {
		t.Fatalf("source Write() error = %v", err)
	}
	assertRead(t, target, "source-to-target")
	if _, err := target.Write([]byte("target-to-source")); err != nil {
		t.Fatalf("target Write() error = %v", err)
	}
	assertRead(t, source, "target-to-source")

	_ = source.Close()
	_ = target.Close()
	waitBrokerResults(t, errorsChannel, false)
}

func TestBrokerRejectsWrongToken(t *testing.T) {
	broker := mustBroker(t, Config{
		WaitTimeout: 100 * time.Millisecond,
		IdleTimeout: time.Second,
		MaxWaiting:  10,
		MaxBytes:    1024,
	})
	sourceToken := bytes.Repeat([]byte{0x42}, tokenSize)
	targetToken := bytes.Repeat([]byte{0x24}, tokenSize)
	sourceServer, sourceClient := net.Pipe()
	targetServer, targetClient := net.Pipe()
	defer sourceClient.Close()
	defer targetClient.Close()

	results := make(chan error, 2)
	go func() { results <- broker.Handle(context.Background(), sourceServer) }()
	go func() { results <- broker.Handle(context.Background(), targetServer) }()
	if err := writeRegistration(sourceClient, "session", roleSource, sourceToken, ""); err != nil {
		t.Fatalf("source registration error = %v", err)
	}
	if err := writeRegistration(targetClient, "session", roleTarget, targetToken, ""); err != nil {
		t.Fatalf("target registration error = %v", err)
	}
	if err := <-results; err == nil {
		t.Fatal("broker accepted a wrong token")
	}
}

func TestBrokerRejectsWrongAccessToken(t *testing.T) {
	broker := mustBroker(t, Config{
		WaitTimeout: time.Second,
		IdleTimeout: time.Second,
		MaxWaiting:  10,
		MaxBytes:    1024,
		AccessToken: "correct-relay-token",
	})
	token := bytes.Repeat([]byte{0x42}, tokenSize)
	server, client := net.Pipe()
	defer client.Close()
	result := make(chan error, 1)
	go func() { result <- broker.Handle(context.Background(), server) }()
	if err := writeRegistration(client, "session", roleSource, token, "wrong-relay-token"); err != nil {
		t.Fatalf("registration error = %v", err)
	}
	if err := <-result; err == nil || err.Error() != "Relay access token is invalid" {
		t.Fatalf("broker error = %v, want Relay access token is invalid", err)
	}
}

func TestBrokerAcceptsAccessToken(t *testing.T) {
	broker := mustBroker(t, Config{
		WaitTimeout: time.Second,
		IdleTimeout: time.Second,
		MaxWaiting:  10,
		MaxBytes:    1024,
		AccessToken: "relay-token",
	})
	token := bytes.Repeat([]byte{0x42}, tokenSize)
	source, target, results := connectPairWithAccessToken(t, broker, "session", token, token, "relay-token")
	defer source.Close()
	defer target.Close()
	if _, err := source.Write([]byte("ok")); err != nil {
		t.Fatalf("source Write() error = %v", err)
	}
	assertRead(t, target, "ok")
	_ = source.Close()
	_ = target.Close()
	waitBrokerResults(t, results, false)
}

func TestBrokerAcceptsProvidedAccessToken(t *testing.T) {
	broker := mustBroker(t, Config{
		WaitTimeout: time.Second,
		IdleTimeout: time.Second,
		MaxWaiting:  10,
		MaxBytes:    1024,
		AccessTokenProvider: func() []string {
			return []string{"customer-a", "customer-b"}
		},
	})
	token := bytes.Repeat([]byte{0x42}, tokenSize)
	source, target, results := connectPairWithAccessToken(t, broker, "session", token, token, "customer-b")
	defer source.Close()
	defer target.Close()
	if _, err := source.Write([]byte("ok")); err != nil {
		t.Fatalf("source Write() error = %v", err)
	}
	assertRead(t, target, "ok")
	_ = source.Close()
	_ = target.Close()
	waitBrokerResults(t, results, false)
}

func TestBrokerSnapshotTracksRelayTraffic(t *testing.T) {
	broker := mustBroker(t, Config{
		WaitTimeout: time.Second,
		IdleTimeout: time.Second,
		MaxWaiting:  10,
		MaxBytes:    1024,
	})
	token := bytes.Repeat([]byte{0x42}, tokenSize)
	source, target, results := connectPair(t, broker, token, token)
	defer source.Close()
	defer target.Close()

	if _, err := source.Write([]byte("source-to-target")); err != nil {
		t.Fatalf("source Write() error = %v", err)
	}
	assertRead(t, target, "source-to-target")
	if _, err := target.Write([]byte("target-to-source")); err != nil {
		t.Fatalf("target Write() error = %v", err)
	}
	assertRead(t, source, "target-to-source")

	snapshot := broker.Snapshot()
	if snapshot.Active != 1 || snapshot.Connections != 2 {
		t.Fatalf("Snapshot active/connections = %d/%d, want 1/2", snapshot.Active, snapshot.Connections)
	}
	if snapshot.TotalSourceBytes != uint64(len("source-to-target")) || snapshot.TotalTargetBytes != uint64(len("target-to-source")) {
		t.Fatalf("Snapshot bytes = %d/%d", snapshot.TotalSourceBytes, snapshot.TotalTargetBytes)
	}

	_ = source.Close()
	_ = target.Close()
	waitBrokerResults(t, results, false)
}

func TestBrokerRejectsDuplicateRole(t *testing.T) {
	broker := mustBroker(t, Config{
		WaitTimeout: 100 * time.Millisecond,
		IdleTimeout: time.Second,
		MaxWaiting:  10,
		MaxBytes:    1024,
	})
	token := bytes.Repeat([]byte{0x42}, tokenSize)
	firstServer, firstClient := net.Pipe()
	secondServer, secondClient := net.Pipe()
	defer firstClient.Close()
	defer secondClient.Close()
	results := make(chan error, 2)
	go func() { results <- broker.Handle(context.Background(), firstServer) }()
	go func() { results <- broker.Handle(context.Background(), secondServer) }()
	_ = writeRegistration(firstClient, "session", roleSource, token, "")
	_ = writeRegistration(secondClient, "session", roleSource, token, "")
	if err := <-results; err == nil {
		t.Fatal("broker accepted duplicate roles")
	}
}

func TestBrokerPairingTimeout(t *testing.T) {
	broker := mustBroker(t, Config{
		WaitTimeout: 20 * time.Millisecond,
		IdleTimeout: time.Second,
		MaxWaiting:  10,
		MaxBytes:    1024,
	})
	server, client := net.Pipe()
	defer client.Close()
	result := make(chan error, 1)
	go func() { result <- broker.Handle(context.Background(), server) }()
	if err := writeRegistration(client, "session", roleSource, make([]byte, tokenSize), ""); err != nil {
		t.Fatalf("writeRegistration() error = %v", err)
	}
	if err := <-result; err == nil {
		t.Fatal("Handle() error = nil, want pairing timeout")
	}
}

func TestBrokerByteLimit(t *testing.T) {
	broker := mustBroker(t, Config{
		WaitTimeout: time.Second,
		IdleTimeout: time.Second,
		MaxWaiting:  10,
		MaxBytes:    4,
	})
	token := bytes.Repeat([]byte{0x42}, tokenSize)
	source, target, results := connectPair(t, broker, token, token)
	defer source.Close()
	defer target.Close()
	_, _ = source.Write([]byte("12345"))
	if err := <-results; err == nil {
		t.Fatal("Relay byte limit did not stop the session")
	}
}

func TestBrokerActiveSessionLimit(t *testing.T) {
	broker := mustBroker(t, Config{
		WaitTimeout: 100 * time.Millisecond,
		IdleTimeout: time.Second,
		MaxWaiting:  10,
		MaxActive:   1,
		MaxBytes:    1024,
	})
	token := bytes.Repeat([]byte{0x42}, tokenSize)
	firstSource, firstTarget, firstResults := connectPairSession(t, broker, "first", token, token)
	defer firstSource.Close()
	defer firstTarget.Close()

	sourceServer, sourceClient := net.Pipe()
	targetServer, targetClient := net.Pipe()
	defer sourceClient.Close()
	defer targetClient.Close()
	results := make(chan error, 2)
	go func() { results <- broker.Handle(context.Background(), sourceServer) }()
	go func() { results <- broker.Handle(context.Background(), targetServer) }()
	if err := writeRegistration(sourceClient, "second", roleSource, token, ""); err != nil {
		t.Fatalf("source registration error = %v", err)
	}
	if err := writeRegistration(targetClient, "second", roleTarget, token, ""); err != nil {
		t.Fatalf("target registration error = %v", err)
	}
	if err := <-results; err == nil || err.Error() != "Relay active session limit reached" {
		t.Fatalf("active limit error = %v", err)
	}

	_ = sourceClient.Close()
	_ = firstSource.Close()
	_ = firstTarget.Close()
	waitBrokerResults(t, firstResults, false)
}

func TestRegisterCanBeCanceled(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Register(ctx, client, "session", RoleSource, make([]byte, tokenSize), "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Register() error = %v, want context.Canceled", err)
	}
}

func TestBrokerRegistrationCanBeCanceled(t *testing.T) {
	broker := mustBroker(t, Config{
		WaitTimeout: time.Second,
		IdleTimeout: time.Second,
		MaxWaiting:  10,
		MaxActive:   10,
		MaxBytes:    1024,
	})
	server, client := net.Pipe()
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- broker.Handle(ctx, server) }()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Handle() error = %v, want context.Canceled", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Handle() did not stop after cancellation")
	}
}

func connectPair(t *testing.T, broker *Broker, sourceToken, targetToken []byte) (net.Conn, net.Conn, chan error) {
	t.Helper()
	return connectPairSession(t, broker, "session", sourceToken, targetToken)
}

func connectPairSession(t *testing.T, broker *Broker, sessionID string, sourceToken, targetToken []byte) (net.Conn, net.Conn, chan error) {
	t.Helper()
	return connectPairWithAccessToken(t, broker, sessionID, sourceToken, targetToken, "")
}

func connectPairWithAccessToken(t *testing.T, broker *Broker, sessionID string, sourceToken, targetToken []byte, accessToken string) (net.Conn, net.Conn, chan error) {
	t.Helper()
	sourceServer, sourceClient := net.Pipe()
	targetServer, targetClient := net.Pipe()
	results := make(chan error, 2)
	go func() { results <- broker.Handle(context.Background(), sourceServer) }()
	go func() { results <- broker.Handle(context.Background(), targetServer) }()

	sourceReady := make(chan error, 1)
	targetReady := make(chan error, 1)
	go func() {
		sourceReady <- Register(context.Background(), sourceClient, sessionID, RoleSource, sourceToken, accessToken)
	}()
	go func() {
		targetReady <- Register(context.Background(), targetClient, sessionID, RoleTarget, targetToken, accessToken)
	}()
	if err := <-sourceReady; err != nil {
		t.Fatalf("source Register() error = %v", err)
	}
	if err := <-targetReady; err != nil {
		t.Fatalf("target Register() error = %v", err)
	}
	return sourceClient, targetClient, results
}

func assertRead(t *testing.T, reader io.Reader, want string) {
	t.Helper()
	data := make([]byte, len(want))
	if _, err := io.ReadFull(reader, data); err != nil {
		t.Fatalf("ReadFull() error = %v", err)
	}
	if string(data) != want {
		t.Fatalf("read = %q, want %q", data, want)
	}
}

func waitBrokerResults(t *testing.T, results chan error, requireError bool) {
	t.Helper()
	for range 2 {
		err := <-results
		if requireError && err == nil {
			t.Fatal("broker result error = nil")
		}
	}
}

func mustBroker(t *testing.T, config Config) *Broker {
	t.Helper()
	broker, err := NewBroker(config)
	if err != nil {
		t.Fatalf("NewBroker() error = %v", err)
	}
	return broker
}
