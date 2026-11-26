// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcpauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/atrest"
)

type recordStore interface {
	putState(ctx context.Context, key string, st *oauthState) error
	takeState(ctx context.Context, key string) (*oauthState, error)
	takeStateByVerifierHash(ctx context.Context, hash string) (*oauthState, error)
	countLiveStates(ctx context.Context) (int, error)
	putCode(ctx context.Context, code string, c *oauthCode) error
	takeCode(ctx context.Context, code string) (*oauthCode, error)
	putRefresh(ctx context.Context, token string, e *mcpRefreshTokenEntry) error
	getRefresh(ctx context.Context, token string) (*mcpRefreshTokenEntry, error)
	rotateRefresh(ctx context.Context, oldToken, newToken string, e *mcpRefreshTokenEntry) (bool, error)
}

type RecordRepo interface {
	PutState(ctx context.Context, gatewayID, stateHash, verifierHash, data string, expiresAt, now int64) error
	TakeState(ctx context.Context, gatewayID, stateHash string, now int64) (string, bool, error)
	TakeStateByVerifier(ctx context.Context, gatewayID, verifierHash string, now int64) (string, bool, error)
	CountLiveStates(ctx context.Context, gatewayID string, now int64) (int, error)
	PutCode(ctx context.Context, gatewayID, codeHash, data string, expiresAt, now int64) error
	TakeCode(ctx context.Context, gatewayID, codeHash string, now int64) (string, bool, error)
	PutRefresh(ctx context.Context, gatewayID, tokenHash, data string, expiresAt, now int64) error
	GetRefresh(ctx context.Context, gatewayID, tokenHash string, now int64) (string, bool, error)
	RotateRefresh(ctx context.Context, gatewayID, oldHash, newHash, data string, expiresAt, now int64) (bool, error)
}

func verifierHash(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return hex.EncodeToString(sum[:])
}

func expired(createdAt time.Time, ttl time.Duration, now time.Time) bool {
	return now.Sub(createdAt) > ttl
}

type memoryStore struct {
	mu            sync.Mutex
	states        map[string]*oauthState
	codes         map[string]*oauthCode
	refreshTokens map[string]*mcpRefreshTokenEntry
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		states:        make(map[string]*oauthState),
		codes:         make(map[string]*oauthCode),
		refreshTokens: make(map[string]*mcpRefreshTokenEntry),
	}
}

func (m *memoryStore) putState(_ context.Context, key string, st *oauthState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupExpiredStatesLocked(time.Now())
	m.states[key] = st
	return nil
}

func (m *memoryStore) takeState(_ context.Context, key string) (*oauthState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.states[key]
	if !ok {
		return nil, nil
	}
	delete(m.states, key)
	if expired(st.CreatedAt, st.TTL, time.Now()) {
		return nil, nil
	}
	return st, nil
}

func (m *memoryStore) takeStateByVerifierHash(_ context.Context, hash string) (*oauthState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for k, st := range m.states {
		if verifierHash(st.CodeVerifier) == hash {
			delete(m.states, k)
			if expired(st.CreatedAt, st.TTL, now) {
				return nil, nil
			}
			return st, nil
		}
	}
	return nil, nil
}

func (m *memoryStore) countLiveStates(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupExpiredStatesLocked(time.Now())
	return len(m.states), nil
}

func (m *memoryStore) putCode(_ context.Context, code string, c *oauthCode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupExpiredCodesLocked(time.Now())
	m.codes[code] = c
	return nil
}

func (m *memoryStore) takeCode(_ context.Context, code string) (*oauthCode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.codes[code]
	if !ok {
		return nil, nil
	}
	delete(m.codes, code)
	if expired(c.CreatedAt, c.TTL, time.Now()) {
		return nil, nil
	}
	return c, nil
}

func (m *memoryStore) putRefresh(_ context.Context, token string, e *mcpRefreshTokenEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupExpiredRefreshTokensLocked(time.Now())
	m.refreshTokens[token] = e
	return nil
}

func (m *memoryStore) getRefresh(_ context.Context, token string) (*mcpRefreshTokenEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.refreshTokens[token]
	if !ok {
		return nil, nil
	}
	if expired(e.CreatedAt, e.TTL, time.Now()) {
		delete(m.refreshTokens, token)
		return nil, nil
	}
	return e, nil
}

func (m *memoryStore) rotateRefresh(_ context.Context, oldToken, newToken string, e *mcpRefreshTokenEntry) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.refreshTokens[oldToken]
	if !ok {
		return false, nil
	}
	delete(m.refreshTokens, oldToken)
	if expired(old.CreatedAt, old.TTL, time.Now()) {
		return false, nil
	}
	m.refreshTokens[newToken] = e
	return true, nil
}

func (m *memoryStore) cleanupExpiredStatesLocked(now time.Time) {
	for k, s := range m.states {
		if expired(s.CreatedAt, s.TTL, now) {
			delete(m.states, k)
		}
	}
}

func (m *memoryStore) cleanupExpiredCodesLocked(now time.Time) {
	for k, c := range m.codes {
		if expired(c.CreatedAt, c.TTL, now) {
			delete(m.codes, k)
		}
	}
}

func (m *memoryStore) cleanupExpiredRefreshTokensLocked(now time.Time) {
	for k, e := range m.refreshTokens {
		if expired(e.CreatedAt, e.TTL, now) {
			delete(m.refreshTokens, k)
		}
	}
}

type dbStore struct {
	repo      RecordRepo
	box       *atrest.Box
	gatewayID string
	logger    *slog.Logger
}

func (s *dbStore) seal(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal record: %w", err)
	}
	return s.box.Seal(b)
}

func open[T any](s *dbStore, sealed string, found bool, err error) (*T, error) {
	if err != nil || !found {
		return nil, err
	}
	b, err := s.box.Open(sealed)
	if err != nil {
		s.logger.Warn("MCP OAuth: login record cannot be opened, treating it as absent", "error", err)
		return nil, nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		s.logger.Warn("MCP OAuth: login record is malformed, treating it as absent", "error", err)
		return nil, nil
	}
	return &v, nil
}

func (s *dbStore) putState(ctx context.Context, key string, st *oauthState) error {
	data, err := s.seal(st)
	if err != nil {
		return err
	}
	return s.repo.PutState(ctx, s.gatewayID, atrest.Hash(key), verifierHash(st.CodeVerifier), data,
		st.CreatedAt.Add(st.TTL).UnixMilli(), st.CreatedAt.UnixMilli())
}

func (s *dbStore) takeState(ctx context.Context, key string) (*oauthState, error) {
	data, ok, err := s.repo.TakeState(ctx, s.gatewayID, atrest.Hash(key), time.Now().UnixMilli())
	return open[oauthState](s, data, ok, err)
}

func (s *dbStore) takeStateByVerifierHash(ctx context.Context, hash string) (*oauthState, error) {
	data, ok, err := s.repo.TakeStateByVerifier(ctx, s.gatewayID, hash, time.Now().UnixMilli())
	return open[oauthState](s, data, ok, err)
}

func (s *dbStore) countLiveStates(ctx context.Context) (int, error) {
	return s.repo.CountLiveStates(ctx, s.gatewayID, time.Now().UnixMilli())
}

func (s *dbStore) putCode(ctx context.Context, code string, c *oauthCode) error {
	data, err := s.seal(c)
	if err != nil {
		return err
	}
	return s.repo.PutCode(ctx, s.gatewayID, atrest.Hash(code), data,
		c.CreatedAt.Add(c.TTL).UnixMilli(), c.CreatedAt.UnixMilli())
}

func (s *dbStore) takeCode(ctx context.Context, code string) (*oauthCode, error) {
	data, ok, err := s.repo.TakeCode(ctx, s.gatewayID, atrest.Hash(code), time.Now().UnixMilli())
	return open[oauthCode](s, data, ok, err)
}

func (s *dbStore) putRefresh(ctx context.Context, token string, e *mcpRefreshTokenEntry) error {
	data, err := s.seal(e)
	if err != nil {
		return err
	}
	return s.repo.PutRefresh(ctx, s.gatewayID, atrest.Hash(token), data,
		e.CreatedAt.Add(e.TTL).UnixMilli(), e.CreatedAt.UnixMilli())
}

func (s *dbStore) getRefresh(ctx context.Context, token string) (*mcpRefreshTokenEntry, error) {
	data, ok, err := s.repo.GetRefresh(ctx, s.gatewayID, atrest.Hash(token), time.Now().UnixMilli())
	return open[mcpRefreshTokenEntry](s, data, ok, err)
}

func (s *dbStore) rotateRefresh(ctx context.Context, oldToken, newToken string, e *mcpRefreshTokenEntry) (bool, error) {
	data, err := s.seal(e)
	if err != nil {
		return false, err
	}
	return s.repo.RotateRefresh(ctx, s.gatewayID, atrest.Hash(oldToken), atrest.Hash(newToken), data,
		e.CreatedAt.Add(e.TTL).UnixMilli(), e.CreatedAt.UnixMilli())
}
