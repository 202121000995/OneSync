package network

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
)

const (
	minimumTokenLength = 32
	identityVersion    = 1
	maxPeerIDLength    = 128
)

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

// AuthenticatePeerClient authenticates a target and presents its stable identity.
func AuthenticatePeerClient(ctx context.Context, session Session, requestID uint64, token []byte, peerID string) error {
	if err := validateToken(token); err != nil {
		return err
	}
	if len(peerID) < 1 || len(peerID) > maxPeerIDLength {
		return errors.New("peer identity length is invalid")
	}
	payload := make([]byte, 3+len(peerID)+len(token))
	payload[0] = identityVersion
	binary.BigEndian.PutUint16(payload[1:3], uint16(len(peerID)))
	copy(payload[3:], peerID)
	copy(payload[3+len(peerID):], token)
	if err := session.Send(ctx, Message{
		Type: MessageAuthenticate, RequestID: requestID, Payload: payload,
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

// AuthenticatePeerServer validates a target token and returns its peer identity.
func AuthenticatePeerServer(ctx context.Context, session Session, expectedToken []byte, expectedPeerID string) (string, error) {
	if err := validateToken(expectedToken); err != nil {
		return "", err
	}
	request, err := session.Receive(ctx)
	if err != nil {
		return "", err
	}
	peerID, token, valid := decodePeerAuthentication(request.Payload)
	valid = valid && request.Type == MessageAuthenticate && tokensEqual(token, expectedToken)
	if expectedPeerID != "" {
		valid = valid && tokensEqual([]byte(peerID), []byte(expectedPeerID))
	}
	responseType := MessageAck
	if !valid {
		responseType = MessageError
	}
	if err := session.Send(ctx, Message{
		Type: responseType, RequestID: request.RequestID,
	}); err != nil {
		return "", err
	}
	if !valid {
		return "", errAuthenticationFailed
	}
	return peerID, nil
}

func decodePeerAuthentication(payload []byte) (string, []byte, bool) {
	if len(payload) < 3+minimumTokenLength || payload[0] != identityVersion {
		return "", nil, false
	}
	peerLength := int(binary.BigEndian.Uint16(payload[1:3]))
	if peerLength < 1 || peerLength > maxPeerIDLength || len(payload) < 3+peerLength+minimumTokenLength {
		return "", nil, false
	}
	return string(payload[3 : 3+peerLength]), payload[3+peerLength:], true
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
