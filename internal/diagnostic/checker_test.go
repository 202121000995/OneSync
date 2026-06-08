package diagnostic

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/202121000995/OneSync/internal/certutil"
)

func TestCheckerVerifiesTLSEndpoint(t *testing.T) {
	serverConfig, clientConfig := diagnosticTLSConfigs(t)
	listener := startDiagnosticTLSServer(t, serverConfig)

	checker, err := NewChecker(clientConfig, time.Second)
	if err != nil {
		t.Fatalf("NewChecker() error = %v", err)
	}
	result := checker.Test(context.Background(), listener.Addr().String(), "")
	if !result.Usable || !result.Direct.OK {
		t.Fatalf("Test() = %+v, want usable direct endpoint", result)
	}
}

func TestCheckerReportsUntrustedCertificate(t *testing.T) {
	serverConfig, _ := diagnosticTLSConfigs(t)
	listener := startDiagnosticTLSServer(t, serverConfig)
	checker, err := NewChecker(&tls.Config{}, time.Second)
	if err != nil {
		t.Fatalf("NewChecker() error = %v", err)
	}
	result := checker.Check(context.Background(), listener.Addr().String())
	if result.OK || result.Error == "" {
		t.Fatalf("Check() = %+v, want verification failure", result)
	}
}

func TestCheckerRejectsDisabledVerification(t *testing.T) {
	if _, err := NewChecker(&tls.Config{InsecureSkipVerify: true}, time.Second); err == nil {
		t.Fatal("NewChecker() accepted disabled certificate verification")
	}
}

func diagnosticTLSConfigs(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	root := t.TempDir()
	certPath := filepath.Join(root, "server.crt")
	keyPath := filepath.Join(root, "server.key")
	if err := certutil.Generate(certutil.Options{
		Hosts:    []string{"127.0.0.1"},
		CertPath: certPath,
		KeyPath:  keyPath,
		Validity: time.Hour,
	}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("LoadX509KeyPair() error = %v", err)
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certPEM) {
		t.Fatal("AppendCertsFromPEM() = false")
	}
	return &tls.Config{Certificates: []tls.Certificate{certificate}}, &tls.Config{RootCAs: roots}
}

func startDiagnosticTLSServer(t *testing.T, config *tls.Config) net.Listener {
	t.Helper()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", config)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			if tlsConnection, ok := connection.(*tls.Conn); ok {
				_ = tlsConnection.Handshake()
			}
			_ = connection.Close()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})
	return listener
}
