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
	if serverTLS != nil {
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
	session, directErr := transport.Connect(ctx, credential.Endpoint)
	directConnected := directErr == nil
	if directErr == nil {
		directErr = network.AuthenticatePeerClient(ctx, session, 1, authenticationToken, peerID)
		if directErr == nil {
			return targetConnection{session: session}, nil
		}
		_ = session.Close()
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
	connection, err := dialer.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return nil, fmt.Errorf("connect Relay TLS endpoint: %w", err)
	}
	if err := relay.Register(ctx, connection, sessionID, role, token, accessToken); err != nil {
		_ = connection.Close()
		return nil, err
	}
	session, err := network.NewSession(connection, maxPayload)
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	return session, nil
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
