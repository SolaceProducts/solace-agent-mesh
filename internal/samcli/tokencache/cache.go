// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tokencache

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/oauth"
)

var ErrNotLoggedIn = errors.New("tokencache: no cached credentials")

type Cached struct {
	PlatformURL    string    `json:"platform_url"`
	Issuer         string    `json:"issuer,omitempty"`
	AccessToken    string    `json:"access_token,omitempty"`
	SAMAccessToken string    `json:"sam_access_token"`
	RefreshToken   string    `json:"refresh_token,omitempty"`
	ExpiresAt      time.Time `json:"expires_at"`
	ObtainedAt     time.Time `json:"obtained_at"`
}

type Store struct {
	Root string
}

func NewDefault() (*Store, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("tokencache: locate user config dir: %w", err)
	}
	return &Store{Root: filepath.Join(dir, "sam", "auth")}, nil
}

func (s *Store) Path(name string) string {
	return filepath.Join(s.Root, sanitizeName(name)+".json")
}

func (s *Store) Load(name string) (*Cached, error) {
	path := s.Path(name)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("tokencache: read %s: %w", path, err)
	}
	var c Cached
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("tokencache: parse %s: %w", path, err)
	}
	return &c, nil
}

func (s *Store) Save(name string, c Cached) error {
	if err := s.ensureRoot(); err != nil {
		return err
	}

	target := s.Path(name)
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("tokencache: marshal: %w", err)
	}

	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	tmpPath := filepath.Join(s.Root, "."+sanitizeName(name)+"."+suffix+".tmp")
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("tokencache: create temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("tokencache: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("tokencache: close temp: %w", err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("tokencache: rename: %w", err)
	}
	return nil
}

func randomSuffix() (string, error) {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("tokencache: rand: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

func (s *Store) Delete(name string) error {
	path := s.Path(name)
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("tokencache: remove %s: %w", path, err)
	}
	return nil
}

func (s *Store) List() ([]string, error) {
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("tokencache: list: %w", err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		out = append(out, strings.TrimSuffix(name, ".json"))
	}
	return out, nil
}

func FromTokens(cfg oauth.Config, t oauth.Tokens) Cached {
	return Cached{
		PlatformURL:    cfg.PlatformURL,
		Issuer:         cfg.Issuer,
		AccessToken:    t.AccessToken,
		SAMAccessToken: t.SAMAccessToken,
		RefreshToken:   t.RefreshToken,
		ExpiresAt:      t.ExpiresAt,
		ObtainedAt:     t.ObtainedAt,
	}
}

func (s *Store) ensureRoot() error {
	if err := os.MkdirAll(s.Root, 0o700); err != nil {
		return fmt.Errorf("tokencache: mkdir %s: %w", s.Root, err)
	}
	if err := os.Chmod(s.Root, 0o700); err != nil {
		return fmt.Errorf("tokencache: chmod %s: %w", s.Root, err)
	}
	return nil
}

func sanitizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "default"
	}
	var b strings.Builder
	b.Grow(len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
			b.WriteByte(c)
		default:
			b.WriteByte('-')
		}
	}
	out := b.String()
	if out == "" {
		return "default"
	}
	return out
}
