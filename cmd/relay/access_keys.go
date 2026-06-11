package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type accessKeyStore struct {
	path string
}

type relayAccessKey struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Token     string    `json:"token"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

func newAccessKeyStore(path string) accessKeyStore {
	return accessKeyStore{path: strings.TrimSpace(path)}
}

func (s accessKeyStore) load() ([]relayAccessKey, error) {
	if s.path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	var keys []relayAccessKey
	if err := json.Unmarshal(data, &keys); err != nil {
		return nil, fmt.Errorf("read Relay access keys: %w", err)
	}
	return keys, nil
}

func (s accessKeyStore) save(keys []relayAccessKey) error {
	if s.path == "" {
		return errors.New("Relay access key file is not configured")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(keys, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(s.path, data, 0o600)
}

func (s accessKeyStore) enabledTokens() []string {
	keys, err := s.load()
	if err != nil {
		return nil
	}
	tokens := make([]string, 0, len(keys))
	for _, key := range keys {
		if key.Enabled && strings.TrimSpace(key.Token) != "" {
			tokens = append(tokens, key.Token)
		}
	}
	return tokens
}

func (s accessKeyStore) create(name string) (relayAccessKey, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "未命名令牌"
	}
	if len(name) > 80 {
		return relayAccessKey{}, errors.New("令牌名称不能超过 80 个字符")
	}
	keys, err := s.load()
	if err != nil {
		return relayAccessKey{}, err
	}
	token, err := randomRelayToken()
	if err != nil {
		return relayAccessKey{}, err
	}
	id, err := randomKeyID()
	if err != nil {
		return relayAccessKey{}, err
	}
	key := relayAccessKey{
		ID:        id,
		Name:      name,
		Token:     token,
		Enabled:   true,
		CreatedAt: time.Now().UTC(),
	}
	keys = append(keys, key)
	if err := s.save(keys); err != nil {
		return relayAccessKey{}, err
	}
	return key, nil
}

func (s accessKeyStore) setEnabled(id string, enabled bool) error {
	keys, err := s.load()
	if err != nil {
		return err
	}
	for index := range keys {
		if keys[index].ID == id {
			keys[index].Enabled = enabled
			return s.save(keys)
		}
	}
	return errors.New("找不到这个 Relay 令牌")
}

func (s accessKeyStore) delete(id string) error {
	keys, err := s.load()
	if err != nil {
		return err
	}
	next := keys[:0]
	found := false
	for _, key := range keys {
		if key.ID == id {
			found = true
			continue
		}
		next = append(next, key)
	}
	if !found {
		return errors.New("找不到这个 Relay 令牌")
	}
	return s.save(next)
}

func randomKeyID() (string, error) {
	token, err := randomRelayToken()
	if err != nil {
		return "", err
	}
	if len(token) > 12 {
		return token[:12], nil
	}
	return token, nil
}
