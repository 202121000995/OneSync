package relay

import (
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
	roleSource                = 1
	roleTarget                = 2
	tokenSize                 = 32
	maxSessionIDLength        = 128
	maxAccessTokenLength      = 512
	registrationHeader        = 4
	registrationHeaderV2      = 6
)

type registration struct {
	sessionID          string
	role               byte
	tokenHash          [sha256.Size]byte
	accessTokenHash    [sha256.Size]byte
	accessTokenPresent bool
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
