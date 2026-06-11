package relay

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	registrationVersion       = 2
	legacyRegistrationVersion = 1
	controlJoinVersion        = 3
	sessionJoinVersion        = 4
	roleSource                = 1
	roleTarget                = 2
	tokenSize                 = 32
	sessionKeySize            = 32
	maxSessionIDLength        = 128
	maxAccessTokenLength      = 512
	maxControlPayloadLength   = 1024
	registrationHeader        = 4
	registrationHeaderV2      = 6
)

const (
	controlMessageRequestSession = 1
	controlMessageInviteSession  = 2
	controlMessageError          = 3
	controlMessagePing           = 4
	controlMessagePong           = 5
	controlMessageWake           = 6
)

type registration struct {
	sessionID          string
	role               byte
	tokenHash          [sha256.Size]byte
	accessTokenHash    [sha256.Size]byte
	accessTokenPresent bool
}

type sessionJoin struct {
	sessionID string
	role      byte
	key       [sessionKeySize]byte
}

type controlMessage struct {
	messageType byte
	payload     []byte
}

func readRegistration(reader io.Reader) (registration, error) {
	header := make([]byte, registrationHeader)
	if _, err := io.ReadFull(reader, header); err != nil {
		return registration{}, fmt.Errorf("read registration header: %w", err)
	}
	if header[0] != registrationVersion && header[0] != legacyRegistrationVersion {
		return registration{}, fmt.Errorf("unsupported registration version %d", header[0])
	}
	if header[1] != roleSource && header[1] != roleTarget {
		return registration{}, errors.New("registration role is invalid")
	}
	sessionLength := int(binary.BigEndian.Uint16(header[2:4]))
	if sessionLength < 1 || sessionLength > maxSessionIDLength {
		return registration{}, errors.New("registration session ID length is invalid")
	}
	accessTokenLength := 0
	if header[0] == registrationVersion {
		extended := make([]byte, registrationHeaderV2-registrationHeader)
		if _, err := io.ReadFull(reader, extended); err != nil {
			return registration{}, fmt.Errorf("read registration access token header: %w", err)
		}
		accessTokenLength = int(binary.BigEndian.Uint16(extended))
		if accessTokenLength > maxAccessTokenLength {
			return registration{}, errors.New("registration access token length is invalid")
		}
	}
	payload := make([]byte, sessionLength+tokenSize+accessTokenLength)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return registration{}, fmt.Errorf("read registration payload: %w", err)
	}
	sessionID := string(payload[:sessionLength])
	if err := validateSessionID(sessionID); err != nil {
		return registration{}, err
	}
	tokenEnd := sessionLength + tokenSize
	tokenHash := sha256.Sum256(payload[sessionLength:tokenEnd])
	var accessTokenHash [sha256.Size]byte
	accessTokenPresent := accessTokenLength > 0
	if accessTokenPresent {
		accessTokenHash = sha256.Sum256(payload[tokenEnd:])
	}
	clear(payload[sessionLength:])
	return registration{
		sessionID:          sessionID,
		role:               header[1],
		tokenHash:          tokenHash,
		accessTokenHash:    accessTokenHash,
		accessTokenPresent: accessTokenPresent,
	}, nil
}

func writeRegistration(writer io.Writer, sessionID string, role byte, token []byte, accessToken string) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	if role != roleSource && role != roleTarget {
		return errors.New("registration role is invalid")
	}
	if len(token) != tokenSize {
		return fmt.Errorf("registration token must contain %d bytes", tokenSize)
	}
	if len(accessToken) > maxAccessTokenLength {
		return errors.New("registration access token is too large")
	}
	header := make([]byte, registrationHeaderV2)
	header[0] = registrationVersion
	header[1] = role
	binary.BigEndian.PutUint16(header[2:4], uint16(len(sessionID)))
	binary.BigEndian.PutUint16(header[4:6], uint16(len(accessToken)))
	if err := writeAll(writer, header); err != nil {
		return err
	}
	if err := writeAll(writer, []byte(sessionID)); err != nil {
		return err
	}
	if err := writeAll(writer, token); err != nil {
		return err
	}
	if accessToken != "" {
		return writeAll(writer, []byte(accessToken))
	}
	return nil
}

func readControlJoin(reader io.Reader) (registration, error) {
	return readRegistrationLike(reader, controlJoinVersion)
}

func writeControlJoin(writer io.Writer, sessionID string, role byte, token []byte, accessToken string) error {
	return writeRegistrationLike(writer, controlJoinVersion, role, sessionID, token, accessToken)
}

func readSessionJoin(reader io.Reader) (sessionJoin, error) {
	header := make([]byte, registrationHeader)
	if _, err := io.ReadFull(reader, header); err != nil {
		return sessionJoin{}, fmt.Errorf("read session join header: %w", err)
	}
	if header[0] != sessionJoinVersion {
		return sessionJoin{}, fmt.Errorf("unsupported session join version %d", header[0])
	}
	if header[1] != roleSource && header[1] != roleTarget {
		return sessionJoin{}, errors.New("session join role is invalid")
	}
	sessionLength := int(binary.BigEndian.Uint16(header[2:4]))
	if sessionLength < 1 || sessionLength > maxSessionIDLength {
		return sessionJoin{}, errors.New("session join session ID length is invalid")
	}
	payload := make([]byte, sessionLength+sessionKeySize)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return sessionJoin{}, fmt.Errorf("read session join payload: %w", err)
	}
	sessionID := string(payload[:sessionLength])
	if err := validateSessionID(sessionID); err != nil {
		return sessionJoin{}, err
	}
	var key [sessionKeySize]byte
	copy(key[:], payload[sessionLength:])
	clear(payload[sessionLength:])
	return sessionJoin{sessionID: sessionID, role: header[1], key: key}, nil
}

func writeSessionJoin(writer io.Writer, sessionID string, role byte, key [sessionKeySize]byte) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	if role != roleSource && role != roleTarget {
		return errors.New("session join role is invalid")
	}
	header := make([]byte, registrationHeader)
	header[0] = sessionJoinVersion
	header[1] = role
	binary.BigEndian.PutUint16(header[2:4], uint16(len(sessionID)))
	if err := writeAll(writer, header); err != nil {
		return err
	}
	if err := writeAll(writer, []byte(sessionID)); err != nil {
		return err
	}
	return writeAll(writer, key[:])
}

func readRegistrationLike(reader io.Reader, version byte) (registration, error) {
	header := make([]byte, registrationHeaderV2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return registration{}, fmt.Errorf("read Relay join header: %w", err)
	}
	if header[0] != version {
		return registration{}, fmt.Errorf("unsupported Relay join version %d", header[0])
	}
	if header[1] != roleSource && header[1] != roleTarget {
		return registration{}, errors.New("Relay join role is invalid")
	}
	sessionLength := int(binary.BigEndian.Uint16(header[2:4]))
	if sessionLength < 1 || sessionLength > maxSessionIDLength {
		return registration{}, errors.New("Relay join session ID length is invalid")
	}
	accessTokenLength := int(binary.BigEndian.Uint16(header[4:6]))
	if accessTokenLength > maxAccessTokenLength {
		return registration{}, errors.New("Relay join access token length is invalid")
	}
	payload := make([]byte, sessionLength+tokenSize+accessTokenLength)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return registration{}, fmt.Errorf("read Relay join payload: %w", err)
	}
	sessionID := string(payload[:sessionLength])
	if err := validateSessionID(sessionID); err != nil {
		return registration{}, err
	}
	tokenEnd := sessionLength + tokenSize
	tokenHash := sha256.Sum256(payload[sessionLength:tokenEnd])
	var accessTokenHash [sha256.Size]byte
	accessTokenPresent := accessTokenLength > 0
	if accessTokenPresent {
		accessTokenHash = sha256.Sum256(payload[tokenEnd:])
	}
	clear(payload[sessionLength:])
	return registration{
		sessionID:          sessionID,
		role:               header[1],
		tokenHash:          tokenHash,
		accessTokenHash:    accessTokenHash,
		accessTokenPresent: accessTokenPresent,
	}, nil
}

func writeRegistrationLike(writer io.Writer, version, role byte, sessionID string, token []byte, accessToken string) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	if role != roleSource && role != roleTarget {
		return errors.New("Relay join role is invalid")
	}
	if len(token) != tokenSize {
		return fmt.Errorf("Relay join token must contain %d bytes", tokenSize)
	}
	if len(accessToken) > maxAccessTokenLength {
		return errors.New("Relay join access token is too large")
	}
	header := make([]byte, registrationHeaderV2)
	header[0] = version
	header[1] = role
	binary.BigEndian.PutUint16(header[2:4], uint16(len(sessionID)))
	binary.BigEndian.PutUint16(header[4:6], uint16(len(accessToken)))
	if err := writeAll(writer, header); err != nil {
		return err
	}
	if err := writeAll(writer, []byte(sessionID)); err != nil {
		return err
	}
	if err := writeAll(writer, token); err != nil {
		return err
	}
	if accessToken != "" {
		return writeAll(writer, []byte(accessToken))
	}
	return nil
}

func readControlMessage(reader io.Reader) (controlMessage, error) {
	header := make([]byte, 3)
	if _, err := io.ReadFull(reader, header); err != nil {
		return controlMessage{}, fmt.Errorf("read Relay control message header: %w", err)
	}
	messageType := header[0]
	if messageType < controlMessageRequestSession || messageType > controlMessageWake {
		return controlMessage{}, fmt.Errorf("Relay control message type %d is invalid", messageType)
	}
	payloadLength := int(binary.BigEndian.Uint16(header[1:3]))
	if payloadLength > maxControlPayloadLength {
		return controlMessage{}, errors.New("Relay control message payload is too large")
	}
	payload := make([]byte, payloadLength)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return controlMessage{}, fmt.Errorf("read Relay control message payload: %w", err)
	}
	return controlMessage{messageType: messageType, payload: payload}, nil
}

func writeControlMessage(writer io.Writer, messageType byte, payload []byte) error {
	if messageType < controlMessageRequestSession || messageType > controlMessageWake {
		return fmt.Errorf("Relay control message type %d is invalid", messageType)
	}
	if len(payload) > maxControlPayloadLength {
		return errors.New("Relay control message payload is too large")
	}
	header := make([]byte, 3)
	header[0] = messageType
	binary.BigEndian.PutUint16(header[1:3], uint16(len(payload)))
	if err := writeAll(writer, header); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	return writeAll(writer, payload)
}

func newSessionKey() ([sessionKeySize]byte, error) {
	var key [sessionKeySize]byte
	if _, err := rand.Read(key[:]); err != nil {
		return key, err
	}
	return key, nil
}

func validateSessionID(sessionID string) error {
	if strings.TrimSpace(sessionID) == "" || len(sessionID) > maxSessionIDLength {
		return errors.New("registration session ID is invalid")
	}
	if strings.ContainsAny(sessionID, `/\`+"\x00") {
		return errors.New("registration session ID contains an unsafe character")
	}
	return nil
}

func sameToken(left, right [sha256.Size]byte) bool {
	return subtle.ConstantTimeCompare(left[:], right[:]) == 1
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if written > 0 {
			data = data[written:]
		}
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
