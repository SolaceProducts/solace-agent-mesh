// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package identity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
)

const STREnricherAgentName = "platform-identity"

const strEnricherCallTimeout = 30 * time.Second

type toolsetLookup interface {
	GetToolsetConfig(ctx context.Context, packageID string) (*store.ToolsetConfig, error)
}

type identityConfigLookup interface {
	Get(ctx context.Context) (*store.PlatformIdentityConfig, error)
}

type toolFileLookup interface {
	ResolveWireToolName(ctx context.Context, packageID, bareName string) (string, error)
}

type TokenSigner interface {
	SignServiceToken(corrID string) (string, error)
}

type STREnricher struct {
	brk          broker.Broker
	namespace    string
	identityCR   identityConfigLookup
	toolsetCR    toolsetLookup
	toolFileRepo toolFileLookup
	signer       TokenSigner
	logger       *slog.Logger

	mu      sync.Mutex
	exe     *remote.Executor
	started bool
}

func NewSTREnricher(
	brk broker.Broker,
	namespace string,
	identityCR identityConfigLookup,
	toolsetCR toolsetLookup,
	toolFileRepo toolFileLookup,
	signer TokenSigner,
	logger *slog.Logger,
) *STREnricher {
	if logger == nil {
		logger = slog.Default()
	}
	return &STREnricher{
		brk:          brk,
		namespace:    namespace,
		identityCR:   identityCR,
		toolsetCR:    toolsetCR,
		toolFileRepo: toolFileRepo,
		signer:       signer,
		logger:       logger.With("logger_name", "identity-enricher"),
	}
}

func (e *STREnricher) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.started {
		return nil
	}
	if e.brk == nil {
		return errors.New("identity enricher: broker is nil")
	}
	if e.namespace == "" {
		return errors.New("identity enricher: namespace is empty")
	}
	exe := remote.NewExecutor(e.brk, e.namespace, STREnricherAgentName)
	if err := exe.Start(ctx); err != nil {
		return fmt.Errorf("identity enricher: start remote executor: %w", err)
	}
	e.exe = exe
	e.started = true
	e.logger.Info("identity enricher started", "namespace", e.namespace, "agentName", STREnricherAgentName)
	return nil
}

func (e *STREnricher) Stop(_ context.Context) error {
	e.mu.Lock()
	exe := e.exe
	e.exe = nil
	e.started = false
	e.mu.Unlock()
	if exe != nil {
		exe.Stop()
	}
	return nil
}

func (e *STREnricher) EnrichAuthenticatedUser(ctx context.Context, lookupValue string) (*Profile, error) {
	result, err := e.invoke(ctx, "enrich_authenticated_user", map[string]any{
		"lookup_value": lookupValue,
	})
	if err != nil {
		return nil, err
	}
	profile, err := extractProfile(result, "profile")
	if err != nil {
		return nil, fmt.Errorf("enrich_authenticated_user: %w", err)
	}
	return profile, nil
}

func (e *STREnricher) SearchUsers(ctx context.Context, query string, limit int) ([]*Profile, error) {
	args := map[string]any{"query": query}
	if limit > 0 {
		args["limit"] = limit
	}
	result, err := e.invoke(ctx, searchUsersTool, args)
	if err != nil {
		return nil, err
	}
	return extractProfileList(result, "profiles", e.logger)
}

func (e *STREnricher) HasTool(ctx context.Context, toolsetID, bareName string) (bool, error) {
	wireName, err := e.toolFileRepo.ResolveWireToolName(ctx, toolsetID, bareName)
	if err != nil {
		return false, fmt.Errorf("resolve wire name for %q: %w", bareName, err)
	}
	return wireName != "", nil
}

func (e *STREnricher) GetUserProfile(ctx context.Context, userID string) (map[string]any, error) {
	result, err := e.invoke(ctx, "get_user_profile", map[string]any{"user_id": userID})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	raw, ok := result["profile"]
	if !ok || raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("get_user_profile: unexpected profile shape %T", raw)
	}
	return m, nil
}

func (e *STREnricher) GetTimeOff(ctx context.Context, userID, start, end string) ([]TimeOffEntry, error) {
	result, err := e.invoke(ctx, "get_time_off", map[string]any{
		"user_id": userID,
		"start":   start,
		"end":     end,
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	raw, ok := result["entries"]
	if !ok || raw == nil {
		return nil, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("get_time_off: re-marshal entries: %w", err)
	}
	var entries []TimeOffEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, fmt.Errorf("get_time_off: decode entries: %w", err)
	}
	return entries, nil
}

func (e *STREnricher) GetUserProfilePicture(ctx context.Context, userID string) ([]byte, string, error) {
	result, err := e.invoke(ctx, "get_user_profile_picture", map[string]any{"user_id": userID})
	if err != nil {
		return nil, "", err
	}
	if result == nil {
		return nil, "", nil
	}
	mime, _ := result["mime_type"].(string)
	b64, _ := result["image_base64"].(string)
	if b64 == "" {
		return nil, mime, nil
	}
	bytes, decodeErr := base64.StdEncoding.DecodeString(b64)
	if decodeErr != nil {
		return nil, "", fmt.Errorf("get_user_profile_picture: decode image: %w", decodeErr)
	}
	return bytes, mime, nil
}

func (e *STREnricher) invoke(ctx context.Context, toolName string, args map[string]any) (map[string]any, error) {
	e.mu.Lock()
	exe := e.exe
	started := e.started
	e.mu.Unlock()
	if !started || exe == nil {
		return nil, errors.New("identity enricher: not started")
	}

	cfg, err := e.identityCR.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("read identity config: %w", err)
	}
	if cfg == nil || cfg.ToolsetID == nil || *cfg.ToolsetID == "" {
		return nil, errors.New("identity enricher: no toolset configured")
	}
	toolsetID := *cfg.ToolsetID

	toolConfig, err := e.loadToolConfig(ctx, toolsetID, toolName)
	if err != nil {
		return nil, fmt.Errorf("load tool_config: %w", err)
	}

	wireName, err := e.resolveWireName(ctx, toolsetID, toolName)
	if err != nil {
		return nil, err
	}

	callCtx, cancel := context.WithTimeout(ctx, strEnricherCallTimeout)
	defer cancel()

	corrID := idgen.New()
	req := &remote.InvokeRequest{
		ToolName:       wireName,
		Args:           args,
		ToolConfig:     toolConfig,
		AppName:        STREnricherAgentName,
		UserID:         "platform-identity",
		SessionID:      corrID,
		TimeoutSeconds: int(strEnricherCallTimeout / time.Second),
	}
	if e.signer != nil {
		token, signErr := e.signer.SignServiceToken(corrID)
		if signErr != nil {
			return nil, fmt.Errorf("mint enrichment auth token: %w", signErr)
		}
		req.AuthToken = token
	}

	resp, err := exe.Invoke(callCtx, corrID, req, nil)
	if err != nil {
		return nil, fmt.Errorf("invoke %s: %w", toolName, err)
	}

	tr := resp.Result.ToolResult
	if tr == nil {
		return nil, fmt.Errorf("invoke %s: empty tool result", toolName)
	}
	if status, _ := tr["status"].(string); status != "" && status != "success" {
		msg, _ := tr["message"].(string)
		return nil, fmt.Errorf("invoke %s: tool reported %s: %s", toolName, status, msg)
	}
	data, _ := tr["data"].(map[string]any)
	return data, nil
}

func (e *STREnricher) resolveWireName(ctx context.Context, toolsetID, bareName string) (string, error) {
	wireName, err := e.toolFileRepo.ResolveWireToolName(ctx, toolsetID, bareName)
	if err != nil {
		return "", fmt.Errorf("resolve wire name for %q: %w", bareName, err)
	}
	if wireName == "" {
		return "", fmt.Errorf("resolve wire name for %q: tool not present in toolset %s", bareName, toolsetID)
	}
	return wireName, nil
}

func (e *STREnricher) loadToolConfig(ctx context.Context, packageID, toolName string) (map[string]any, error) {
	row, err := e.toolsetCR.GetToolsetConfig(ctx, packageID)
	if err != nil {
		return nil, err
	}
	if row == nil || row.ConfigValues == nil || *row.ConfigValues == "" {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(*row.ConfigValues), &m); err != nil {
		return nil, fmt.Errorf("decode tool_config json: %w", err)
	}
	if m == nil {
		return map[string]any{}, nil
	}

	if perTool, ok := m[toolName].(map[string]any); ok {
		return perTool, nil
	}
	for _, v := range m {
		if _, isMap := v.(map[string]any); isMap {
			return map[string]any{}, nil
		}
	}
	return m, nil
}

func extractProfile(result map[string]any, key string) (*Profile, error) {
	if result == nil {
		return nil, nil
	}
	raw, ok := result[key]
	if !ok || raw == nil {
		return nil, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("re-marshal %s: %w", key, err)
	}
	var p Profile
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("decode %s: %w", key, err)
	}
	return &p, nil
}

func extractProfileList(result map[string]any, key string, logger *slog.Logger) ([]*Profile, error) {
	if result == nil {
		return []*Profile{}, nil
	}
	raw, ok := result[key]
	if !ok || raw == nil {
		return []*Profile{}, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("re-marshal %s: %w", key, err)
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, fmt.Errorf("decode %s: %w", key, err)
	}
	profiles := make([]*Profile, 0, len(rows))
	for i, row := range rows {
		var p Profile
		if err := json.Unmarshal(row, &p); err != nil {
			if logger != nil {
				logger.Warn("skipping malformed profile in search results", "index", i, "error", err)
			}
			continue
		}
		profiles = append(profiles, &p)
	}
	return profiles, nil
}

var _ Enricher = (*STREnricher)(nil)
