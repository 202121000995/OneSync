package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

const (
	LinkVersion    = 1
	DefaultLinkTTL = 24 * time.Hour
	linkTokenBytes = 32
	maxEncodedLink = 16 << 10
)

// Link contains the public connection metadata carried between devices.
type Link struct {
	Version          int       `json:"version"`
	SessionID        string    `json:"session_id"`
	Endpoint         string    `json:"endpoint"`
	RelayEndpoint    string    `json:"relay_endpoint,omitempty"`
	CACertificatePEM string    `json:"ca_certificate_pem,omitempty"`
	Token            string    `json:"token"`
	IssuedAt         time.Time `json:"issued_at"`
	ExpiresAt        time.Time `json:"expires_at"`
}

type issuedLink struct {
	tokenHash [sha256.Size]byte
	expiresAt time.Time
	used      bool
}

// LinkService issues, parses, and redeems one-time synchronization links.
type LinkService struct {
	mu     sync.Mutex
	links  map[string]issuedLink
	now    func() time.Time
	random func([]byte) (int, error)
}

// NewLinkService creates an in-memory one-time link registry.
func NewLinkService() *LinkService {
	return &LinkService{
		links:  make(map[string]issuedLink),
		now:    time.Now,
		random: rand.Read,
	}
}

// Issue creates a link valid for 24 hours.
func (s *LinkService) Issue(sessionID, endpoint, relayEndpoint string) (string, error) {
	return s.IssueWithCertificate(sessionID, endpoint, relayEndpoint, "")
}

// IssueWithCertificate creates a link and optionally includes a public CA certificate.
func (s *LinkService) IssueWithCertificate(sessionID, endpoint, relayEndpoint, caCertificatePEM string) (string, error) {
	if err := validateLinkMetadata(sessionID, endpoint, relayEndpoint, caCertificatePEM); err != nil {
		return "", err
	}
	tokenBytes := make([]byte, linkTokenBytes)
	if _, err := s.random(tokenBytes); err != nil {
		return "", fmt.Errorf("generate synchronization token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	now := s.now().UTC()
	link := Link{
		Version:          LinkVersion,
		SessionID:        sessionID,
		Endpoint:         endpoint,
		RelayEndpoint:    relayEndpoint,
		CACertificatePEM: caCertificatePEM,
		Token:            token,
		IssuedAt:         now,
		ExpiresAt:        now.Add(DefaultLinkTTL),
	}
	data, err := json.Marshal(link)
	if err != nil {
		return "", fmt.Errorf("encode synchronization link: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(data)

	s.mu.Lock()
	s.removeExpiredLocked(now)
	s.links[sessionID] = issuedLink{
		tokenHash: sha256.Sum256([]byte(token)),
		expiresAt: link.ExpiresAt,
	}
	s.mu.Unlock()
	return encoded, nil
}

// Parse decodes public link metadata without consuming the one-time token.
func (s *LinkService) Parse(encoded string) (Link, error) {
	if encoded == "" || len(encoded) > maxEncodedLink {
		return Link{}, errors.New("synchronization link length is invalid")
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return Link{}, errors.New("synchronization link is malformed")
	}
	var link Link
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&link); err != nil {
		return Link{}, errors.New("synchronization link is malformed")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Link{}, errors.New("synchronization link is malformed")
	}
	if link.Version != LinkVersion {
		return Link{}, fmt.Errorf("unsupported synchronization link version %d", link.Version)
	}
	if err := validateLinkMetadata(link.SessionID, link.Endpoint, link.RelayEndpoint, link.CACertificatePEM); err != nil {
		return Link{}, err
	}
	tokenBytes, err := base64.RawURLEncoding.DecodeString(link.Token)
	if err != nil || len(tokenBytes) != linkTokenBytes {
		return Link{}, errors.New("synchronization link token is invalid")
	}
	if link.IssuedAt.IsZero() || link.ExpiresAt.Sub(link.IssuedAt) != DefaultLinkTTL {
		return Link{}, errors.New("synchronization link expiry is invalid")
	}
	return link, nil
}

// Redeem validates and consumes an issued token. A second redemption fails.
func (s *LinkService) Redeem(encoded string) (Link, error) {
	link, err := s.Parse(encoded)
	if err != nil {
		return Link{}, err
	}
	now := s.now().UTC()
	if !now.Before(link.ExpiresAt) {
		return Link{}, errors.New("synchronization link has expired")
	}
	hash := sha256.Sum256([]byte(link.Token))

	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeExpiredLocked(now)
	issued, exists := s.links[link.SessionID]
	if !exists || issued.used || !now.Before(issued.expiresAt) ||
		subtle.ConstantTimeCompare(hash[:], issued.tokenHash[:]) != 1 {
		return Link{}, errors.New("synchronization link is invalid or already used")
	}
	issued.used = true
	s.links[link.SessionID] = issued
	return link, nil
}

func (s *LinkService) removeExpiredLocked(now time.Time) {
	for sessionID, link := range s.links {
		if !now.Before(link.expiresAt) {
			delete(s.links, sessionID)
		}
	}
}

func validateLinkMetadata(sessionID, endpoint, relayEndpoint, caCertificatePEM string) error {
	if strings.TrimSpace(sessionID) == "" || len(sessionID) > 128 {
		return errors.New("session ID is invalid")
	}
	if strings.ContainsAny(sessionID, `/\`+"\x00") {
		return errors.New("session ID contains an unsafe character")
	}
	if strings.TrimSpace(endpoint) == "" || len(endpoint) > 2048 {
		return errors.New("connection endpoint is invalid")
	}
	if len(relayEndpoint) > 2048 {
		return errors.New("relay endpoint is invalid")
	}
	if caCertificatePEM != "" {
		if len(caCertificatePEM) > 8192 {
			return errors.New("CA certificate is too large")
		}
		rest := []byte(caCertificatePEM)
		found := false
		for {
			rest = bytes.TrimSpace(rest)
			if len(rest) == 0 {
				break
			}
			var block *pem.Block
			block, rest = pem.Decode(rest)
			if block == nil || block.Type != "CERTIFICATE" {
				return errors.New("CA certificate is invalid")
			}
			if _, err := x509.ParseCertificate(block.Bytes); err != nil {
				return errors.New("CA certificate is invalid")
			}
			found = true
		}
		if !found {
			return errors.New("CA certificate is invalid")
		}
	}
	return nil
}
