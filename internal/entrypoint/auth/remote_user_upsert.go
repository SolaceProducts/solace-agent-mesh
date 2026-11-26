// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type UserRecord struct {
	Issuer       string
	Sub          string
	OIDCProvider string
	DisplayName  string
	UserName     string
	Email        string
	Phone        string
	Groups       []string
	Source       string
}

type UserUpsertClient interface {
	Upsert(ctx context.Context, rec UserRecord) (UserUpsertResult, error)
}

type UserUpsertResult struct {
	ID           string
	RolesChanged bool
}

type userUpsertRequest struct {
	Issuer       string   `json:"issuer"`
	Sub          string   `json:"sub"`
	OIDCProvider string   `json:"oidc_provider,omitempty"`
	DisplayName  string   `json:"display_name,omitempty"`
	UserName     string   `json:"user_name,omitempty"`
	Email        string   `json:"email,omitempty"`
	Phone        string   `json:"phone,omitempty"`
	Groups       []string `json:"groups,omitempty"`
	Source       string   `json:"source,omitempty"`
	GatewayID    string   `json:"gateway_id,omitempty"`
}

type BrokerUserUpsertClient struct {
	broker       broker.Broker
	namespace    string
	gatewayID    string
	timeout      time.Duration
	maxAttempts  int
	retryBackoff time.Duration
	logger       *slog.Logger
}

type BrokerUserUpsertClientConfig struct {
	Namespace string
	GatewayID string
	Timeout   time.Duration

	MaxAttempts int

	RetryBackoff time.Duration
}

func NewBrokerUserUpsertClient(brk broker.Broker, cfg BrokerUserUpsertClientConfig, logger *slog.Logger) *BrokerUserUpsertClient {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 2 * time.Second
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 2
	}
	if cfg.RetryBackoff <= 0 {
		cfg.RetryBackoff = 250 * time.Millisecond
	}
	return &BrokerUserUpsertClient{
		broker:       brk,
		namespace:    cfg.Namespace,
		gatewayID:    cfg.GatewayID,
		timeout:      cfg.Timeout,
		maxAttempts:  cfg.MaxAttempts,
		retryBackoff: cfg.RetryBackoff,
		logger:       logger.With("logger_name", "user-upsert-client"),
	}
}

func (c *BrokerUserUpsertClient) Upsert(ctx context.Context, rec UserRecord) (UserUpsertResult, error) {
	if rec.Issuer == "" || rec.Sub == "" {
		return UserUpsertResult{}, fmt.Errorf("user upsert client: issuer and sub are required")
	}
	payload, err := json.Marshal(userUpsertRequest{
		Issuer:       rec.Issuer,
		Sub:          rec.Sub,
		OIDCProvider: rec.OIDCProvider,
		DisplayName:  rec.DisplayName,
		UserName:     rec.UserName,
		Email:        rec.Email,
		Phone:        rec.Phone,
		Groups:       rec.Groups,
		Source:       rec.Source,
		GatewayID:    c.gatewayID,
	})
	if err != nil {
		return UserUpsertResult{}, fmt.Errorf("user upsert client: marshal request: %w", err)
	}

	var lastErr error
	for attempt := range c.maxAttempts {
		if attempt > 0 {
			if err := sleepCtx(ctx, c.retryBackoff*time.Duration(attempt)); err != nil {
				return UserUpsertResult{}, fmt.Errorf("user upsert client: canceled: %w", err)
			}
		}
		res, retryable, err := c.upsertOnce(ctx, payload)
		if err == nil {
			return res, nil
		}
		lastErr = err
		if !retryable {
			return UserUpsertResult{}, err
		}
	}
	return UserUpsertResult{}, lastErr
}

func (c *BrokerUserUpsertClient) upsertOnce(ctx context.Context, payload []byte) (res UserUpsertResult, retryable bool, err error) {
	correlationID := idgen.New()
	msg := &broker.Message{
		Payload:       payload,
		ReplyTo:       a2a.UserUpsertResponseTopic(c.namespace, c.gatewayID) + "/" + correlationID,
		CorrelationID: correlationID,
	}
	reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	reply, err := c.broker.Request(reqCtx, a2a.UserUpsertTopic(c.namespace, c.gatewayID), msg, c.timeout)
	if err != nil {
		return UserUpsertResult{}, true, fmt.Errorf("user upsert client: request: %w", err)
	}

	resp, err := control.UnmarshalResponse(reply.Payload)
	if err != nil {
		return UserUpsertResult{}, false, fmt.Errorf("user upsert client: unmarshal reply: %w", err)
	}
	if resp.Error != nil {
		return UserUpsertResult{}, false, fmt.Errorf("user upsert denied: %s (code=%d)", resp.Error.Message, resp.Error.Code)
	}
	if m, ok := resp.Result.(map[string]any); ok {
		if v, ok := m["id"].(string); ok {
			res.ID = v
		}
		res.RolesChanged, _ = m["roles_changed"].(bool)
	}
	if res.ID == "" {
		return UserUpsertResult{}, false, fmt.Errorf("user upsert client: reply missing string id field; got %T", resp.Result)
	}
	return res, false, nil
}
