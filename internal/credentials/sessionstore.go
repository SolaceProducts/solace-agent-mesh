// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package credentials

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	identity "github.com/SolaceProducts/solace-agent-mesh/internal/agentidentity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

const (
	pythonSecretsTTL        = 30 * 24 * time.Hour
	errGetCredentialSession = "get credential session: %w"
)

type SessionStore struct {
	store      session.Store
	keyManager *identity.KeyManager
	logger     *slog.Logger
	defaultTTL time.Duration
}

func NewSessionStore(store session.Store, km *identity.KeyManager, logger *slog.Logger) *SessionStore {
	if logger == nil {
		logger = slog.Default()
	}
	return &SessionStore{
		store:      store,
		keyManager: km,
		logger:     logger.With("logger_name", "credential-store"),
		defaultTTL: pythonSecretsTTL,
	}
}

var _ Service = (*SessionStore)(nil)

func (s *SessionStore) Seal(plaintext string) (string, error) {
	encrypted, err := identity.Encrypt(s.keyManager.EncryptionKey(), []byte(plaintext))
	if err != nil {
		return "", fmt.Errorf("encrypt credential: %w", err)
	}
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

func (s *SessionStore) Open(sealed string) (string, error) {
	encrypted, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", fmt.Errorf("decode credential: %w", err)
	}
	plaintext, err := identity.Decrypt(s.keyManager.EncryptionKey(), encrypted)
	if err != nil {
		return "", fmt.Errorf("decrypt credential: %w", err)
	}
	return string(plaintext), nil
}

func (s *SessionStore) Get(ctx context.Context, scope Scope, key string) (string, error) {
	appName := secretAppName(scope.AgentName)
	sessionID := deriveSessionID(s.keyManager.IdentityKey(), appName, scope.UserID, key)

	sess, err := s.store.Get(ctx, appName, scope.UserID, sessionID)
	if err != nil {
		return "", fmt.Errorf(errGetCredentialSession, err)
	}
	if sess == nil {
		return "", nil
	}
	return s.secretValue(sess, scope, key)
}

func (s *SessionStore) secretValue(sess *session.Session, scope Scope, key string) (string, error) {
	if isExpired(sess.Metadata) {
		s.logger.Debug("credential expired", "key", key, "user", scope.UserID)
		return "", nil
	}

	if _, deleted := sess.Metadata["expired_at"]; deleted {
		return "", nil
	}

	encB64, ok := sess.Metadata["secret_value"].(string)
	if !ok || encB64 == "" {
		return "", nil
	}

	encrypted, err := base64.StdEncoding.DecodeString(encB64)
	if err != nil {
		return "", fmt.Errorf("decode credential: %w", err)
	}

	plaintext, err := identity.Decrypt(s.keyManager.EncryptionKey(), encrypted)
	if err != nil {
		return "", fmt.Errorf("decrypt credential: %w", err)
	}

	return string(plaintext), nil
}

func (s *SessionStore) Set(ctx context.Context, scope Scope, key string, value string, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = s.defaultTTL
	}

	appName := secretAppName(scope.AgentName)
	sessionID := deriveSessionID(s.keyManager.IdentityKey(), appName, scope.UserID, key)

	metadata, err := s.secretMetadata(appName, key, value, ttl)
	if err != nil {
		return err
	}

	sess := &session.Session{
		AppName:   appName,
		UserID:    scope.UserID,
		SessionID: sessionID,
		Metadata:  metadata,
	}

	if err := s.store.Create(ctx, sess); err != nil {
		existing, getErr := s.store.Get(ctx, appName, scope.UserID, sessionID)
		if getErr != nil {
			return fmt.Errorf("get existing credential: %w", getErr)
		}
		if existing == nil {
			return fmt.Errorf("credential create failed but not found: %w", err)
		}
		markReplaced(metadata, existing)
		existing.Metadata = metadata
		if saveErr := s.store.Save(ctx, existing); saveErr != nil {
			return fmt.Errorf("update credential: %w", saveErr)
		}
	}

	return nil
}

func (s *SessionStore) Delete(ctx context.Context, scope Scope, key string) error {
	appName := secretAppName(scope.AgentName)
	sessionID := deriveSessionID(s.keyManager.IdentityKey(), appName, scope.UserID, key)

	sess, err := s.store.Get(ctx, appName, scope.UserID, sessionID)
	if err != nil {
		return fmt.Errorf("get credential for delete: %w", err)
	}
	if sess == nil {
		return nil
	}

	markRevoked(sess, reasonRevokedByUser)

	if err := s.store.Save(ctx, sess); err != nil {
		return fmt.Errorf("soft-delete credential: %w", err)
	}

	return nil
}

func (s *SessionStore) GetVersioned(ctx context.Context, scope Scope, key string) (auth.VersionedCredential, error) {
	appName := secretAppName(scope.AgentName)
	sessionID := deriveSessionID(s.keyManager.IdentityKey(), appName, scope.UserID, key)

	sess, err := s.store.Get(ctx, appName, scope.UserID, sessionID)
	if err != nil {
		return auth.VersionedCredential{}, fmt.Errorf(errGetCredentialSession, err)
	}
	if sess == nil {
		return auth.VersionedCredential{}, nil
	}
	value, err := s.secretValue(sess, scope, key)
	if err != nil {
		return auth.VersionedCredential{}, err
	}
	return auth.VersionedCredential{
		Value:        value,
		Version:      sess.Version,
		Found:        true,
		Disconnected: sess.Metadata["expired_reason"] == reasonRevokedByUser,
	}, nil
}

func (s *SessionStore) SetAt(ctx context.Context, scope Scope, key, value string, ttl time.Duration, read auth.VersionedCredential) (auth.VersionedCredential, error) {
	if ttl <= 0 {
		ttl = s.defaultTTL
	}
	appName := secretAppName(scope.AgentName)
	sessionID := deriveSessionID(s.keyManager.IdentityKey(), appName, scope.UserID, key)

	metadata, err := s.secretMetadata(appName, key, value, ttl)
	if err != nil {
		return auth.VersionedCredential{}, err
	}

	if !read.Found {
		created := &session.Session{
			AppName:   appName,
			UserID:    scope.UserID,
			SessionID: sessionID,
			Metadata:  metadata,
		}
		createErr := s.store.Create(ctx, created)
		if errors.Is(createErr, session.ErrSessionExists) {
			return auth.VersionedCredential{}, auth.ErrCredentialChanged
		}
		if createErr != nil {
			return auth.VersionedCredential{}, fmt.Errorf("create credential: %w", createErr)
		}
		return auth.VersionedCredential{Value: value, Version: created.Version, Found: true}, nil
	}

	existing, err := s.rowAtVersion(ctx, appName, scope.UserID, sessionID, read)
	if err != nil {
		return auth.VersionedCredential{}, err
	}
	markReplaced(metadata, existing)
	existing.Metadata = metadata
	if err := s.saveAtVersion(ctx, existing, "update credential"); err != nil {
		return auth.VersionedCredential{}, err
	}
	return auth.VersionedCredential{Value: value, Version: existing.Version, Found: true}, nil
}

func (s *SessionStore) DeleteAt(ctx context.Context, scope Scope, key string, read auth.VersionedCredential) error {
	if !read.Found {
		return nil
	}
	appName := secretAppName(scope.AgentName)
	sessionID := deriveSessionID(s.keyManager.IdentityKey(), appName, scope.UserID, key)

	existing, err := s.rowAtVersion(ctx, appName, scope.UserID, sessionID, read)
	if err != nil {
		return err
	}
	markRevoked(existing, reasonClearedByWriter)
	return s.saveAtVersion(ctx, existing, "soft-delete credential")
}

func (s *SessionStore) rowAtVersion(ctx context.Context, appName, userID, sessionID string, read auth.VersionedCredential) (*session.Session, error) {
	existing, err := s.store.Get(ctx, appName, userID, sessionID)
	if err != nil {
		return nil, fmt.Errorf(errGetCredentialSession, err)
	}
	if existing == nil || existing.Version != read.Version {
		return nil, auth.ErrCredentialChanged
	}
	return existing, nil
}

func (s *SessionStore) saveAtVersion(ctx context.Context, sess *session.Session, action string) error {
	err := s.store.Save(ctx, sess)
	if errors.Is(err, session.ErrOptimisticLock) {
		return auth.ErrCredentialChanged
	}
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	return nil
}

func (s *SessionStore) secretMetadata(appName, key, value string, ttl time.Duration) (map[string]any, error) {
	encrypted, err := identity.Encrypt(s.keyManager.EncryptionKey(), []byte(value))
	if err != nil {
		return nil, fmt.Errorf("encrypt credential: %w", err)
	}
	return map[string]any{
		"type":           "secret",
		"agent_identity": s.keyManager.IdentityKey(),
		"app_name":       appName,
		"key_name":       key,
		"created_at":     time.Now().UTC().Format(time.RFC3339),
		"ttl_seconds":    int(ttl.Seconds()),
		"secret_value":   base64.StdEncoding.EncodeToString(encrypted),
	}, nil
}

func markReplaced(metadata map[string]any, existing *session.Session) {
	if prevCreated, ok := existing.Metadata["created_at"]; ok {
		metadata["replaced_at"] = time.Now().UTC().Format(time.RFC3339)
		metadata["prev_created_at"] = prevCreated
		metadata["prev_expired_reason"] = "force_expire"
	}
}

const (
	reasonRevokedByUser   = "credential_revoked"
	reasonClearedByWriter = "credential_cleared"
)

func markRevoked(sess *session.Session, reason string) {
	sess.Metadata["expired_at"] = time.Now().UTC().Format(time.RFC3339)
	sess.Metadata["expired_reason"] = reason
	delete(sess.Metadata, "secret_value")
}

func (s *SessionStore) List(ctx context.Context, scope Scope) ([]string, error) {
	appName := secretAppName(scope.AgentName)

	sessions, err := s.store.ListSessions(ctx, appName, scope.UserID)
	if err != nil {
		return nil, fmt.Errorf("list credential sessions: %w", err)
	}

	var keys []string
	for _, sess := range sessions {
		if sess.Metadata == nil {
			continue
		}
		if _, deleted := sess.Metadata["expired_at"]; deleted {
			continue
		}
		if isExpired(sess.Metadata) {
			continue
		}
		if keyName, ok := sess.Metadata["key_name"].(string); ok && keyName != "" {
			keys = append(keys, keyName)
		}
	}

	return keys, nil
}

func secretAppName(agentName string) string {
	return "secret_" + agentName
}

func deriveSessionID(agentIdentity, appName, userID, keyName string) string {
	unique := fmt.Sprintf("%s:%s:%s:%s", agentIdentity, appName, userID, keyName)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(unique)).String() //nolint:forbidigo // intentional UUIDv5 for Python parity
}

func isExpired(metadata map[string]any) bool {
	createdStr, ok := metadata["created_at"].(string)
	if !ok {
		return false
	}
	ttlSec, ok := metadata["ttl_seconds"]
	if !ok {
		return false
	}

	var ttlSeconds float64
	switch v := ttlSec.(type) {
	case int:
		ttlSeconds = float64(v)
	case float64:
		ttlSeconds = v
	default:
		return false
	}

	created, err := time.Parse(time.RFC3339, createdStr)
	if err != nil {
		return false
	}

	return time.Now().After(created.Add(time.Duration(ttlSeconds) * time.Second))
}
