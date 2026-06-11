package webauth

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

// Store persists the management-page account.
type Store struct {
	path string
	mu   sync.Mutex
}

// Config stores the configured management account.
type Config struct {
	Username     string `json:"username"`
	Salt         string `json:"salt"`
	PasswordHash string `json:"password_hash"`
}

// NewStore creates a management auth store.
func NewStore(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("web auth path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve web auth path: %w", err)
	}
	return &Store{path: absolute}, nil
}

// Configured reports whether a management account exists.
func (s *Store) Configured() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.loadLocked()
	return err == nil
}

// Username returns the configured management username when available.
func (s *Store) Username() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	config, err := s.loadLocked()
	if err != nil {
		return ""
	}
	return config.Username
}

// Setup writes the first management account.
func (s *Store) Setup(username, password string) error {
	username = strings.TrimSpace(username)
	if err := validateUsername(username); err != nil {
		return err
	}
	if err := validatePassword(password); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.loadLocked(); err == nil {
		return errors.New("management account is already configured")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("generate password salt: %w", err)
	}
	config := Config{
		Username:     username,
		Salt:         base64.RawURLEncoding.EncodeToString(salt),
		PasswordHash: passwordHash(salt, password),
	}
	return s.saveLocked(config)
}

// Verify checks a management username and password.
func (s *Store) Verify(username, password string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	config, err := s.loadLocked()
	if err != nil {
		return false
	}
	return verifyConfig(config, username, password)
}

// ChangePassword changes the management password after checking the existing credentials.
func (s *Store) ChangePassword(username, currentPassword, newPassword string) error {
	username = strings.TrimSpace(username)
	if err := validateUsername(username); err != nil {
		return err
	}
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	config, err := s.loadLocked()
	if err != nil {
		return err
	}
	if !verifyConfig(config, username, currentPassword) {
		return errors.New("current username or password is incorrect")
	}
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("generate password salt: %w", err)
	}
	config.Salt = base64.RawURLEncoding.EncodeToString(salt)
	config.PasswordHash = passwordHash(salt, newPassword)
	return s.saveLocked(config)
}

func (s *Store) loadLocked() (Config, error) {
	info, err := os.Lstat(s.path)
	if err != nil {
		return Config{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<10 {
		return Config{}, errors.New("web auth path is invalid")
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return Config{}, err
	}
	var config Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("web auth file contains multiple JSON values")
	}
	if err := validateConfig(config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (s *Store) saveLocked(config Config) error {
	if err := validateConfig(config); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create web auth directory: %w", err)
	}
	data, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("encode web auth: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".web-auth-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary web auth: %w", err)
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
	return os.Rename(tempPath, s.path)
}

func validateConfig(config Config) error {
	if err := validateUsername(config.Username); err != nil {
		return err
	}
	if _, err := base64.RawURLEncoding.DecodeString(config.Salt); err != nil {
		return errors.New("web auth salt is invalid")
	}
	if _, err := hex.DecodeString(config.PasswordHash); err != nil || len(config.PasswordHash) != sha256.Size*2 {
		return errors.New("web auth password hash is invalid")
	}
	return nil
}

func verifyConfig(config Config, username, password string) bool {
	if subtle.ConstantTimeCompare([]byte(username), []byte(config.Username)) != 1 {
		return false
	}
	salt, err := base64.RawURLEncoding.DecodeString(config.Salt)
	if err != nil {
		return false
	}
	actual := passwordHash(salt, password)
	return subtle.ConstantTimeCompare([]byte(actual), []byte(config.PasswordHash)) == 1
}

func validateUsername(username string) error {
	if strings.TrimSpace(username) == "" || len(username) > 64 {
		return errors.New("username must be 1-64 characters")
	}
	if strings.ContainsRune(username, '\x00') {
		return errors.New("username contains null byte")
	}
	return nil
}

func validatePassword(password string) error {
	if len(password) < 8 || len(password) > 256 {
		return errors.New("password must be 8-256 characters")
	}
	if strings.ContainsRune(password, '\x00') {
		return errors.New("password contains null byte")
	}
	return nil
}

func passwordHash(salt []byte, password string) string {
	round := append([]byte(nil), salt...)
	round = append(round, []byte(password)...)
	sum := sha256.Sum256(round)
	for range 120_000 {
		next := sha256.New()
		next.Write(salt)
		next.Write(sum[:])
		next.Write([]byte(password))
		copy(sum[:], next.Sum(nil))
	}
	return hex.EncodeToString(sum[:])
}
