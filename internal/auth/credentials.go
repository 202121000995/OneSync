package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Credential contains private connection material for one task.
type Credential struct {
	SessionID        string `json:"session_id"`
	Endpoint         string `json:"endpoint"`
	RelayEndpoint    string `json:"relay_endpoint,omitempty"`
	CACertificatePEM string `json:"ca_certificate_pem,omitempty"`
	Token            string `json:"token"`
	PeerID           string `json:"peer_id,omitempty"`
	OneTime          bool   `json:"one_time,omitempty"`
	Used             bool   `json:"used,omitempty"`
}

// CredentialStore persists private task credentials separately from task state.
type CredentialStore struct {
	directory string
	mu        sync.Mutex
}

// NewCredentialStore creates a credential store rooted at directory.
func NewCredentialStore(directory string) (*CredentialStore, error) {
	if strings.TrimSpace(directory) == "" {
		return nil, errors.New("credential directory is required")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve credential directory: %w", err)
	}
	return &CredentialStore{directory: absolute}, nil
}

// Save writes one credential using a private atomic file.
func (s *CredentialStore) Save(taskID string, credential Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(taskID, credential)
}

func (s *CredentialStore) saveLocked(taskID string, credential Credential) error {
	if err := validateCredential(credential); err != nil {
		return err
	}
	if err := os.MkdirAll(s.directory, 0o700); err != nil {
		return fmt.Errorf("create credential directory: %w", err)
	}
	if err := os.Chmod(s.directory, 0o700); err != nil {
		return fmt.Errorf("secure credential directory: %w", err)
	}
	data, err := json.Marshal(credential)
	if err != nil {
		return fmt.Errorf("encode credential: %w", err)
	}
	temp, err := os.CreateTemp(s.directory, ".credential-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary credential: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(append(data, '\n')); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := replaceCredentialFile(tempPath, s.path(taskID)); err != nil {
		return fmt.Errorf("replace credential: %w", err)
	}
	return nil
}

// Load reads one private task credential.
func (s *CredentialStore) Load(taskID string) (Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked(taskID)
}

func (s *CredentialStore) loadLocked(taskID string) (Credential, error) {
	path := s.path(taskID)
	info, err := os.Lstat(path)
	if err != nil {
		return Credential{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return Credential{}, errors.New("credential path is invalid")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Credential{}, err
	}
	var credential Credential
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&credential); err != nil {
		return Credential{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Credential{}, errors.New("credential contains multiple JSON values")
		}
		return Credential{}, err
	}
	if err := validateCredential(credential); err != nil {
		return Credential{}, err
	}
	return credential, nil
}

// Claim binds a one-time credential to the first authenticated peer.
func (s *CredentialStore) Claim(taskID, token, peerID string) (Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	credential, err := s.loadLocked(taskID)
	if err != nil {
		return Credential{}, err
	}
	expected := sha256.Sum256([]byte(credential.Token))
	actual := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
		return Credential{}, errors.New("credential is invalid or already used")
	}
	if credential.Used {
		if peerID == "" || credential.PeerID == "" ||
			subtle.ConstantTimeCompare([]byte(peerID), []byte(credential.PeerID)) != 1 {
			return Credential{}, errors.New("credential is invalid or already used")
		}
		return credential, nil
	}
	if credential.OneTime {
		if err := validatePeerID(peerID); err != nil {
			return Credential{}, err
		}
		credential.Used = true
		credential.PeerID = peerID
		if err := s.saveLocked(taskID, credential); err != nil {
			return Credential{}, err
		}
	}
	return credential, nil
}

// UnbindPeer clears a previously claimed peer identity while preserving link material.
func (s *CredentialStore) UnbindPeer(taskID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	credential, err := s.loadLocked(taskID)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	credential.Used = false
	credential.PeerID = ""
	return s.saveLocked(taskID, credential)
}

// NewPeerID creates a stable high-entropy identity for one target task.
func NewPeerID() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate peer identity: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

// Delete removes a task credential.
func (s *CredentialStore) Delete(taskID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(s.path(taskID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *CredentialStore) path(taskID string) string {
	sum := sha256Bytes(taskID)
	return filepath.Join(s.directory, hex.EncodeToString(sum[:])+".json")
}

func validateCredential(credential Credential) error {
	if err := validateLinkMetadata(credential.SessionID, credential.Endpoint, credential.RelayEndpoint, credential.CACertificatePEM); err != nil {
		return err
	}
	token, err := base64Token(credential.Token)
	if err != nil || len(token) != linkTokenBytes {
		return errors.New("credential token is invalid")
	}
	if credential.PeerID != "" {
		if err := validatePeerID(credential.PeerID); err != nil {
			return err
		}
	}
	if credential.Used && credential.PeerID == "" {
		return errors.New("used credential requires a peer identity")
	}
	return nil
}

func validatePeerID(peerID string) error {
	decoded, err := base64.RawURLEncoding.DecodeString(peerID)
	if err != nil || len(decoded) != 32 {
		return errors.New("peer identity is invalid")
	}
	return nil
}

func sha256Bytes(value string) [32]byte {
	return sha256.Sum256([]byte(value))
}

func base64Token(value string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(value)
}
