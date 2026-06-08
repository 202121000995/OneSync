package network

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
)

const minimumTokenLength = 32

// AuthenticateClient authenticates a client session with a synchronization token.
func AuthenticateClient(ctx context.Context, session Session, requestID uint64, token []byte) error {
	if err := validateToken(token); err != nil {
		return err
	}
	if err := session.Send(ctx, Message{
		Type:      MessageAuthenticate,
		RequestID: requestID,
		Payload:   append([]byte(nil), token...),
	}); err != nil {
		return err
	}

	response, err := session.Receive(ctx)
	if err != nil {
		return err
	}
	if response.RequestID != requestID || response.Type != MessageAck {
		return errAuthenticationFailed
	}
	return nil
}

// AuthenticateServer validates one client token and returns a generic result.
func AuthenticateServer(ctx context.Context, session Session, expectedToken []byte) error {
	if err := validateToken(expectedToken); err != nil {
		return err
	}

	request, err := session.Receive(ctx)
	if err != nil {
		return err
	}
	valid := request.Type == MessageAuthenticate && tokensEqual(request.Payload, expectedToken)

	responseType := MessageAck
	if !valid {
		responseType = MessageError
	}
	if err := session.Send(ctx, Message{
		Type:      responseType,
		RequestID: request.RequestID,
	}); err != nil {
		return err
	}
	if !valid {
		return errAuthenticationFailed
	}
	return nil
}

func validateToken(token []byte) error {
	if len(token) < minimumTokenLength {
		return errors.New("synchronization token must contain at least 32 bytes")
	}
	return nil
}

func tokensEqual(left, right []byte) bool {
	leftHash := sha256.Sum256(left)
	rightHash := sha256.Sum256(right)
	return subtle.ConstantTimeCompare(leftHash[:], rightHash[:]) == 1
}
