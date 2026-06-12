package client

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"

	"github.com/202121000995/OneSync/internal/auth"
	"github.com/202121000995/OneSync/internal/network"
	"github.com/202121000995/OneSync/internal/relay"
)

var errConnectionUnavailable = errors.New("connection is temporarily unavailable")

type connectionResult struct {
	connection authenticatedConnection
	err        error
}

type authenticatedConnection struct {
	session network.Session
	peerID  string
	relayed bool
}

type targetConnection struct {
	session network.Session
	relayed bool
}

func connectSource(
	ctx context.Context,
	credential auth.Credential,
	relayToken, authenticationToken []byte,
	expectedPeerID string,
	serverTLS, clientTLS *tls.Config,
	maxPayload uint32,
) (authenticatedConnection, error) {
	var direct func(context.Context) (authenticatedConnection, error)
	if serverTLS != nil && directEndpointUsable(credential.Endpoint) {
		listenAddress, err := sourceListenAddress(credential.Endpoint)
		if err != nil {
			return authenticatedConnection{}, err
		}
		listener, err := network.ListenTLS(listenAddress, serverTLS, maxPayload)
		if err != nil {
			return authenticatedConnection{}, err
		}
		defer listener.Close()
		direct = func(ctx context.Context) (authenticatedConnection, error) {
			for {
				session, err := listener.Accept(ctx)
				if err != nil {
					return authenticatedConnection{}, err
				}
				peerID, err := network.AuthenticatePeerServer(ctx, session, authenticationToken, expectedPeerID)
				if err == nil {
					return authenticatedConnection{session: session, peerID: peerID}, nil
				}
				_ = session.Close()
				if ctx.Err() != nil {
					return authenticatedConnection{}, ctx.Err()
				}
			}
		}
	}
	var relayed func(context.Context) (authenticatedConnection, error)
	if credential.RelayEndpoint != "" {
		relayed = func(ctx context.Context) (authenticatedConnection, error) {
			session, err := connectRelay(ctx, credential.RelayEndpoint, credential.SessionID, relay.RoleSource, relayToken, credential.RelayToken, clientTLS, maxPayload)
			if err != nil {
				return authenticatedConnection{}, fmt.Errorf("%w: %v", errConnectionUnavailable, err)
			}
			peerID, err := network.AuthenticatePeerServer(ctx, session, authenticationToken, expectedPeerID)
			if err != nil {
				_ = session.Close()
				return authenticatedConnection{}, err
			}
			return authenticatedConnection{session: session, peerID: peerID, relayed: true}, nil
		}
	}
	return firstConnection(ctx, direct, relayed)
}

func connectTarget(
	ctx context.Context,
	credential auth.Credential,
	relayToken, authenticationToken []byte,
	peerID string,
	clientTLS *tls.Config,
	maxPayload uint32,
) (targetConnection, error) {
	transport, err := network.NewTLSTransport(clientTLS, maxPayload)
	if err != nil {
		return targetConnection{}, err
	}
	var directErr error = errors.New("direct endpoint is not usable")
	directConnected := false
	if directEndpointUsable(credential.Endpoint) {
		session, err := transport.Connect(ctx, credential.Endpoint)
		directErr = err
		directConnected = err == nil
		if err == nil {
			directErr = network.AuthenticatePeerClient(ctx, session, 1, authenticationToken, peerID)
			if directErr == nil {
				return targetConnection{session: session}, nil
			}
			_ = session.Close()
		}
	}
	if credential.RelayEndpoint == "" {
		if directConnected {
			return targetConnection{}, directErr
		}
		return targetConnection{}, fmt.Errorf("%w: %v", errConnectionUnavailable, directErr)
	}
	session, relayErr := connectRelay(
		ctx, credential.RelayEndpoint, credential.SessionID, relay.RoleTarget, relayToken, credential.RelayToken, clientTLS, maxPayload,
	)
	if relayErr != nil {
		if directConnected {
			return targetConnection{}, fmt.Errorf("direct authentication failed: %v; Relay connection failed: %w", directErr, relayErr)
		}
		return targetConnection{}, fmt.Errorf("%w: direct connection failed: %v; Relay connection failed: %v", errConnectionUnavailable, directErr, relayErr)
	}
	if err := network.AuthenticatePeerClient(ctx, session, 1, authenticationToken, peerID); err != nil {
		_ = session.Close()
		return targetConnection{}, fmt.Errorf("authenticate through Relay: %w", err)
	}
	return targetConnection{session: session, relayed: true}, nil
}

func connectRelay(
	ctx context.Context,
	endpoint, sessionID, role string,
	token []byte,
	accessToken string,
	config *tls.Config,
	maxPayload uint32,
) (network.Session, error) {
	dialer := tls.Dialer{NetDialer: &net.Dialer{}, Config: config.Clone()}
	controlConnection, err := dialer.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return nil, fmt.Errorf("connect Relay TLS endpoint: %w", err)
	}
	control, err := relay.JoinControl(ctx, controlConnection, sessionID, role, token, accessToken)
	if err != nil {
		_ = controlConnection.Close()
		return nil, err
	}
	var sessionKey [32]byte
	if role == relay.RoleSource {
		sessionKey, err = control.RequestSession(ctx)
	} else {
		sessionKey, err = control.WaitSession(ctx)
	}
	if err != nil {
		_ = control.Close()
		return nil, err
	}
	dataConnection, err := dialer.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		_ = control.Close()
		return nil, fmt.Errorf("connect Relay data endpoint: %w", err)
	}
	if err := relay.JoinSession(ctx, dataConnection, sessionID, role, sessionKey); err != nil {
		_ = dataConnection.Close()
		_ = control.Close()
		return nil, err
	}
	session, err := network.NewSession(dataConnection, maxPayload)
	if err != nil {
		_ = dataConnection.Close()
		_ = control.Close()
		return nil, err
	}
	return relayControlledSession{Session: session, control: control}, nil
}

type relayControlConnection struct {
	endpoint   string
	sessionID  string
	role       string
	config     *tls.Config
	maxPayload uint32
	control    *relay.ControlClient
}

func connectRelayControl(
	ctx context.Context,
	endpoint, sessionID, role string,
	token []byte,
	accessToken string,
	config *tls.Config,
	maxPayload uint32,
) (*relayControlConnection, error) {
	dialer := tls.Dialer{NetDialer: &net.Dialer{}, Config: config.Clone()}
	controlConnection, err := dialer.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return nil, fmt.Errorf("connect Relay TLS endpoint: %w", err)
	}
	control, err := relay.JoinControl(ctx, controlConnection, sessionID, role, token, accessToken)
	if err != nil {
		_ = controlConnection.Close()
		return nil, err
	}
	return &relayControlConnection{
		endpoint:   endpoint,
		sessionID:  sessionID,
		role:       role,
		config:     config.Clone(),
		maxPayload: maxPayload,
		control:    control,
	}, nil
}

func (c *relayControlConnection) OpenSession(ctx context.Context) (network.Session, error) {
	var sessionKey [32]byte
	var err error
	if c.role == relay.RoleSource {
		sessionKey, err = c.control.RequestSession(ctx)
	} else {
		sessionKey, err = c.control.WaitSession(ctx)
	}
	if err != nil {
		return nil, err
	}
	dialer := tls.Dialer{NetDialer: &net.Dialer{}, Config: c.config.Clone()}
	dataConnection, err := dialer.DialContext(ctx, "tcp", c.endpoint)
	if err != nil {
		return nil, fmt.Errorf("connect Relay data endpoint: %w", err)
	}
	if err := relay.JoinSession(ctx, dataConnection, c.sessionID, c.role, sessionKey); err != nil {
		_ = dataConnection.Close()
		return nil, err
	}
	session, err := network.NewSession(dataConnection, c.maxPayload)
	if err != nil {
		_ = dataConnection.Close()
		return nil, err
	}
	return session, nil
}

func (c *relayControlConnection) SendWake(ctx context.Context) error {
	if c == nil || c.control == nil {
		return nil
	}
	return c.control.SendWake(ctx)
}

func (c *relayControlConnection) WaitWake(ctx context.Context) error {
	if c == nil || c.control == nil {
		return errConnectionUnavailable
	}
	return c.control.WaitWake(ctx)
}

func (c *relayControlConnection) Close() error {
	if c == nil || c.control == nil {
		return nil
	}
	return c.control.Close()
}

type relayControlledSession struct {
	network.Session
	control *relay.ControlClient
}

func (s relayControlledSession) Close() error {
	sessionErr := s.Session.Close()
	controlErr := s.control.Close()
	if sessionErr != nil {
		return sessionErr
	}
	return controlErr
}

func (s relayControlledSession) SendWake(ctx context.Context) error {
	if s.control == nil {
		return nil
	}
	return s.control.SendWake(ctx)
}

func (s relayControlledSession) WaitWake(ctx context.Context) error {
	if s.control == nil {
		return errConnectionUnavailable
	}
	return s.control.WaitWake(ctx)
}

func firstConnection(
	ctx context.Context,
	first, second func(context.Context) (authenticatedConnection, error),
) (authenticatedConnection, error) {
	if first == nil && second == nil {
		return authenticatedConnection{}, errors.New("task has no usable direct or Relay connection")
	}
	if first == nil {
		return second(ctx)
	}
	if second == nil {
		return first(ctx)
	}

	connectContext, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan connectionResult, 2)
	go func() {
		connection, err := first(connectContext)
		results <- connectionResult{connection: connection, err: err}
	}()
	go func() {
		connection, err := second(connectContext)
		results <- connectionResult{connection: connection, err: err}
	}()

	firstResult := <-results
	if firstResult.err == nil {
		cancel()
		closeLateSession(results)
		return firstResult.connection, nil
	}
	secondResult := <-results
	if secondResult.err == nil {
		cancel()
		return secondResult.connection, nil
	}
	return authenticatedConnection{}, fmt.Errorf("direct and Relay connections failed: %v; %w", firstResult.err, secondResult.err)
}

func closeLateSession(results <-chan connectionResult) {
	go func() {
		result := <-results
		if result.connection.session != nil {
			_ = result.connection.session.Close()
		}
	}()
}

func sourceListenAddress(endpoint string) (string, error) {
	_, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse source endpoint: %w", err)
	}
	return net.JoinHostPort("", port), nil
}

func directEndpointUsable(endpoint string) bool {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return false
	}
	if port == "" || port == "0" {
		return false
	}
	return host != ""
}
