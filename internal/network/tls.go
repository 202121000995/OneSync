package network

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// TLSTransport establishes certificate-verified TLS 1.3 sessions.
type TLSTransport struct {
	config     *tls.Config
	maxPayload uint32
	dialer     net.Dialer
}

// NewTLSTransport validates and copies a client TLS configuration.
func NewTLSTransport(config *tls.Config, maxPayload uint32) (*TLSTransport, error) {
	if config == nil {
		return nil, errors.New("TLS config is required")
	}
	if config.InsecureSkipVerify {
		return nil, errors.New("TLS certificate verification cannot be disabled")
	}
	if _, err := NewCodec(maxPayload); err != nil {
		return nil, err
	}

	copied := config.Clone()
	copied.MinVersion = tls.VersionTLS13
	return &TLSTransport{config: copied, maxPayload: maxPayload}, nil
}

// Connect establishes a TLS session and completes the TLS handshake.
func (t *TLSTransport) Connect(ctx context.Context, endpoint string) (Session, error) {
	dialer := tls.Dialer{NetDialer: &t.dialer, Config: t.config.Clone()}
	connection, err := dialer.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return nil, fmt.Errorf("connect TLS endpoint: %w", err)
	}

	session, err := NewSession(connection, t.maxPayload)
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	return session, nil
}

// TLSListener accepts TLS 1.3 sessions.
type TLSListener struct {
	listener   *net.TCPListener
	config     *tls.Config
	maxPayload uint32
	acceptMu   sync.Mutex
}

// ListenTLS starts a TLS listener with a configured certificate.
func ListenTLS(address string, config *tls.Config, maxPayload uint32) (*TLSListener, error) {
	if config == nil || len(config.Certificates) == 0 {
		return nil, errors.New("TLS server certificate is required")
	}
	if _, err := NewCodec(maxPayload); err != nil {
		return nil, err
	}

	copied := config.Clone()
	copied.MinVersion = tls.VersionTLS13
	tcpAddress, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("resolve TLS listen address: %w", err)
	}
	listener, err := net.ListenTCP("tcp", tcpAddress)
	if err != nil {
		return nil, fmt.Errorf("listen TLS: %w", err)
	}
	return &TLSListener{listener: listener, config: copied, maxPayload: maxPayload}, nil
}

// Accept waits for and handshakes one TLS client.
func (l *TLSListener) Accept(ctx context.Context) (Session, error) {
	l.acceptMu.Lock()
	defer l.acceptMu.Unlock()

	deadline, hasDeadline := ctx.Deadline()
	if hasDeadline {
		if err := l.listener.SetDeadline(deadline); err != nil {
			return nil, err
		}
	}
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		select {
		case <-ctx.Done():
			_ = l.listener.SetDeadline(time.Now())
		case <-done:
		}
	}()
	connection, err := l.listener.AcceptTCP()
	close(done)
	<-finished
	_ = l.listener.SetDeadline(time.Time{})

	if ctxErr := ctx.Err(); ctxErr != nil {
		if connection != nil {
			_ = connection.Close()
		}
		return nil, ctxErr
	}
	if err != nil {
		var netErr net.Error
		if hasDeadline && errors.As(err, &netErr) && netErr.Timeout() && !time.Now().Before(deadline) {
			return nil, context.DeadlineExceeded
		}
		return nil, err
	}

	tlsConnection := tls.Server(connection, l.config.Clone())
	if err := tlsConnection.HandshakeContext(ctx); err != nil {
		_ = tlsConnection.Close()
		return nil, fmt.Errorf("TLS handshake: %w", err)
	}
	session, err := NewSession(tlsConnection, l.maxPayload)
	if err != nil {
		_ = tlsConnection.Close()
		return nil, err
	}
	return session, nil
}

// Addr returns the listener network address.
func (l *TLSListener) Addr() net.Addr {
	return l.listener.Addr()
}

// Close stops the listener.
func (l *TLSListener) Close() error {
	return l.listener.Close()
}
