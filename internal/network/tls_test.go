package network

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func TestTLSTransportConnectsWithVerifiedCertificate(t *testing.T) {
	serverConfig, clientConfig := testTLSConfigs(t)
	listener, err := ListenTLS("127.0.0.1:0", serverConfig, 1024)
	if err != nil {
		t.Fatalf("ListenTLS() error = %v", err)
	}
	defer listener.Close()

	serverErrors := make(chan error, 1)
	go func() {
		session, err := listener.Accept(context.Background())
		if err != nil {
			serverErrors <- err
			return
		}
		defer session.Close()
		message, err := session.Receive(context.Background())
		if err == nil {
			err = session.Send(context.Background(), Message{
				Type:      MessagePong,
				RequestID: message.RequestID,
			})
		}
		serverErrors <- err
	}()

	transport, err := NewTLSTransport(clientConfig, 1024)
	if err != nil {
		t.Fatalf("NewTLSTransport() error = %v", err)
	}
	session, err := transport.Connect(context.Background(), listener.Addr().String())
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer session.Close()

	if err := session.Send(context.Background(), Message{Type: MessagePing, RequestID: 12}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	response, err := session.Receive(context.Background())
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if response.Type != MessagePong || response.RequestID != 12 {
		t.Fatalf("Receive() = %+v, want pong request 12", response)
	}
	if err := <-serverErrors; err != nil {
		t.Fatalf("server error = %v", err)
	}
}

func TestTLSTransportRejectsDisabledVerification(t *testing.T) {
	_, err := NewTLSTransport(&tls.Config{InsecureSkipVerify: true}, 1024)
	if err == nil {
		t.Fatal("NewTLSTransport() error = nil, want verification error")
	}
}

func TestTLSListenerAcceptCanBeCanceled(t *testing.T) {
	serverConfig, _ := testTLSConfigs(t)
	listener, err := ListenTLS("127.0.0.1:0", serverConfig, 1024)
	if err != nil {
		t.Fatalf("ListenTLS() error = %v", err)
	}
	defer listener.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := listener.Accept(ctx); err != context.DeadlineExceeded {
		t.Fatalf("Accept() error = %v, want context.DeadlineExceeded", err)
	}
}

func testTLSConfigs(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "OneSync Test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey() error = %v", err)
	}

	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})
	certificate, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair() error = %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificatePEM) {
		t.Fatal("AppendCertsFromPEM() = false")
	}

	return &tls.Config{
			Certificates: []tls.Certificate{certificate},
		}, &tls.Config{
			RootCAs:    roots,
			ServerName: "localhost",
		}
}
