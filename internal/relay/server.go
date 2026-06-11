package relay

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// Server accepts TLS 1.3 Relay clients.
type Server struct {
	listener net.Listener
	broker   *Broker
}

// Listen starts a TLS 1.3 Relay listener.
func Listen(address string, tlsConfig *tls.Config, broker *Broker) (*Server, error) {
	if tlsConfig == nil || (len(tlsConfig.Certificates) == 0 && tlsConfig.GetCertificate == nil) {
		return nil, errors.New("Relay TLS certificate is required")
	}
	if broker == nil {
		return nil, errors.New("Relay broker is required")
	}
	config := tlsConfig.Clone()
	config.MinVersion = tls.VersionTLS13
	listener, err := tls.Listen("tcp", address, config)
	if err != nil {
		return nil, fmt.Errorf("listen Relay TLS: %w", err)
	}
	return &Server{listener: listener, broker: broker}, nil
}

// Serve accepts clients until the context is canceled.
func (s *Server) Serve(ctx context.Context) error {
	serveContext, cancel := context.WithCancel(ctx)
	defer cancel()
	var wait sync.WaitGroup
	defer wait.Wait()
	go func() {
		<-serveContext.Done()
		_ = s.listener.Close()
	}()
	for {
		connection, err := s.listener.Accept()
		if err != nil {
			if serveContext.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Temporary() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return err
		}
		wait.Add(1)
		go func() {
			defer wait.Done()
			_ = s.broker.Handle(serveContext, connection)
		}()
	}
}

// Addr returns the listener address.
func (s *Server) Addr() net.Addr {
	return s.listener.Addr()
}

// Close stops accepting clients.
func (s *Server) Close() error {
	return s.listener.Close()
}
