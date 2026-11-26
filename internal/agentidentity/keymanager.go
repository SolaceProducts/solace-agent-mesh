// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
)

type Config struct {
	Mode string `yaml:"key_mode"`

	Key string `yaml:"key_identity"`

	FilePath string `yaml:"key_persistence"`
}

func DefaultConfig() Config {
	return Config{
		Mode: "auto",
	}
}

type KeyManager struct {
	identityKey string
	encKey      []byte
	agentName   string
}

func NewKeyManager(cfg Config, agentName string, sessionStore session.Store, logger *slog.Logger) (*KeyManager, error) {
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("logger_name", "identity-key-manager", "agentName", agentName)

	mode := strings.ToLower(cfg.Mode)
	if mode == "" {
		mode = "auto"
	}

	var identityKey string
	var err error

	switch mode {
	case "auto":
		identityKey, err = autoResolveKey(cfg, agentName, sessionStore, logger)
	case "manual":
		identityKey, err = manualResolveKey(cfg)
	default:
		return nil, fmt.Errorf("unsupported key_mode: %q (expected auto or manual)", mode)
	}
	if err != nil {
		return nil, err
	}

	encKey := deriveEncryptionKeyLikePython(identityKey)

	fingerprint := sha256.Sum256(encKey[:])
	logger.Info("agent identity key initialized",
		"mode", mode,
		"keyFingerprint", hex.EncodeToString(fingerprint[:])[:8],
	)

	return &KeyManager{
		identityKey: identityKey,
		encKey:      encKey[:],
		agentName:   agentName,
	}, nil
}

func deriveEncryptionKeyLikePython(identityKey string) [32]byte {
	return sha256.Sum256([]byte(identityKey))
}

func (km *KeyManager) IdentityKey() string { return km.identityKey }

func (km *KeyManager) EncryptionKey() []byte { return km.encKey }

func (km *KeyManager) AgentName() string { return km.agentName }

func autoResolveKey(cfg Config, agentName string, store session.Store, logger *slog.Logger) (string, error) {
	filePath := resolveFilePath(cfg, agentName)
	if store == nil {
		return resolveFileKey(filePath, logger)
	}

	ctx := context.Background()
	stored, err := loadKeyFromDB(ctx, store, agentName)
	if err != nil {
		return "", fmt.Errorf("load identity key from database: %w", err)
	}
	if stored != "" {
		logger.Info("loaded identity key from database")
		return stored, nil
	}

	candidate, err := loadKeyFromFile(filePath)
	if err != nil || candidate == "" {
		if candidate, err = generateKey(); err != nil {
			return "", fmt.Errorf("generate identity key: %w", err)
		}
	}

	saveErr := saveKeyToDB(ctx, store, agentName, candidate)
	if saveErr == nil {
		logger.Info("saved identity key to database")
		return candidate, nil
	}
	if !errors.Is(saveErr, session.ErrSessionExists) {
		return "", fmt.Errorf("save identity key to database: %w", saveErr)
	}
	stored, err = loadKeyFromDB(ctx, store, agentName)
	if err != nil {
		return "", fmt.Errorf("load identity key another replica saved: %w", err)
	}
	if stored == "" {
		return "", fmt.Errorf("identity key record for %q exists in the database but holds no key", agentName)
	}
	logger.Info("loaded identity key another replica saved to the database")
	return stored, nil
}

func resolveFileKey(filePath string, logger *slog.Logger) (string, error) {
	key, err := loadKeyFromFile(filePath)
	if err == nil && key != "" {
		logger.Info("loaded identity key from file", "path", filePath)
		return key, nil
	}
	newKey, err := generateKey()
	if err != nil {
		return "", fmt.Errorf("generate identity key: %w", err)
	}
	if err := saveKeyToFile(newKey, filePath); err != nil {
		return "", fmt.Errorf("save identity key to file: %w", err)
	}
	logger.Info("saved new identity key to file", "path", filePath)
	return newKey, nil
}

func manualResolveKey(cfg Config) (string, error) {
	key := strings.TrimSpace(cfg.Key)
	if len(key) < 16 {
		return "", fmt.Errorf("manual identity key must be at least 16 characters, got %d", len(key))
	}
	return key, nil
}

func generateKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func resolveFilePath(cfg Config, agentName string) string {
	if cfg.FilePath != "" {
		return cfg.FilePath
	}
	base := "keys"
	if dataDir := os.Getenv("SAM_DATA_DIR"); dataDir != "" {
		base = filepath.Join(dataDir, "keys")
	}
	return filepath.Join(base, fmt.Sprintf("_agent_%s.key", agentName))
}

func dbSessionKey(agentName string) (appName, userID, sessionID string) {
	sessionID = uuid.NewSHA1(uuid.NameSpaceOID, []byte("agent_identity:"+agentName)).String() //nolint:forbidigo // intentional UUIDv5 for a stable, Postgres-uuid-compatible identity-key session id
	return agentName, "system_" + agentName, sessionID
}

func loadKeyFromDB(ctx context.Context, store session.Store, agentName string) (string, error) {
	appName, userID, sessionID := dbSessionKey(agentName)
	sess, err := store.Get(ctx, appName, userID, sessionID)
	if err != nil {
		return "", err
	}
	if sess == nil {
		return "", nil
	}
	key, _ := sess.Metadata["agent_identity_key"].(string)
	return key, nil
}

func saveKeyToDB(ctx context.Context, store session.Store, agentName, key string) error {
	appName, userID, sessionID := dbSessionKey(agentName)
	sess := &session.Session{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		Metadata: map[string]any{
			"agent_identity_key": key,
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	return store.Create(ctx, sess)
}

func loadKeyFromFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func saveKeyToFile(key, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create key directory: %w", err)
	}
	return os.WriteFile(path, []byte(key+"\n"), 0600)
}
