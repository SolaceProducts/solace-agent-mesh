// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmeshprofile

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func logger() *slog.Logger {
	return slog.Default().With("logger_name", "event_mesh_profile_provider")
}

type Provider struct {
	cfg *Config
}

func NewProvider(cfg *Config) *Provider {
	return &Provider{cfg: cfg}
}

func (p *Provider) Lookup(ctx context.Context, lookupValue string) (*identity.Profile, error) {
	lookupValue = strings.ToLower(strings.TrimSpace(lookupValue))
	if lookupValue == "" {
		return nil, nil
	}

	requestID := idgen.New()
	requestTopic := p.cfg.BuildRequestTopic(requestID)
	replyTopic := p.cfg.BuildReplyTopic(requestID)

	payload, err := json.Marshal(map[string]any{
		p.cfg.PayloadKey: lookupValue,
		"replyTopic":     replyTopic,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request payload: %w", err)
	}

	b, err := newBroker(ctx, p.cfg)
	if err != nil {
		return nil, fmt.Errorf("build broker: %w", err)
	}
	if err = b.Connect(ctx); err != nil {
		logger().Error("broker connect failed", "requestID", requestID, "requestTopic", requestTopic, "replyTopic", replyTopic, "error", err)
		return nil, fmt.Errorf("broker connect: %w", err)
	}
	defer func() { _ = b.Disconnect(context.Background()) }()

	reply, err := p.requestReply(ctx, b, requestTopic, replyTopic, requestID, payload)
	if err != nil {
		return nil, err
	}

	rawProfile, err := decodeReplyPayload(reply.Payload)
	if err != nil {
		return nil, fmt.Errorf("decode reply payload: %w", err)
	}
	if len(rawProfile) == 0 {
		return nil, nil
	}
	if msg := detectErrorEnvelope(rawProfile); msg != "" {
		return nil, fmt.Errorf("responder returned error envelope: %s", msg)
	}

	mapped := p.cfg.FieldMappingConfig.Apply(rawProfile)
	if !hasProfileEvidence(mapped) {
		return nil, fmt.Errorf("reply produced no canonical profile fields after field_mapping_config; raw keys=%v", mapKeys(rawProfile))
	}
	profile, err := mappedToProfile(mapped, lookupValue)
	if err != nil {
		return nil, fmt.Errorf("convert mapped reply to Profile: %w", err)
	}
	return profile, nil
}

func (p *Provider) requestReply(
	ctx context.Context,
	b broker.Broker,
	requestTopic, replyTopic, requestID string,
	payload []byte,
) (*broker.Message, error) {
	replyQueue, err := b.CreateDirectSubscription()
	if err != nil {
		return nil, fmt.Errorf("create reply subscription: %w", err)
	}
	defer func() {
		if delErr := b.DeleteQueue(replyQueue.Name()); delErr != nil {
			logger().Debug("delete reply subscription failed", "requestID", requestID, "replyTopic", replyTopic, "queue", replyQueue.Name(), "error", delErr)
		}
	}()

	if err = b.Subscribe(replyQueue, replyTopic); err != nil {
		logger().Error("subscribe reply topic failed", "requestID", requestID, "requestTopic", requestTopic, "replyTopic", replyTopic, "error", err)
		return nil, fmt.Errorf("subscribe reply topic: %w", err)
	}

	msg := &broker.Message{
		Payload:       payload,
		ReplyTo:       replyTopic,
		CorrelationID: requestID,
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, p.cfg.RequestTimeout())
	defer cancel()

	if err = b.PublishGuaranteed(timeoutCtx, requestTopic, msg); err != nil {
		logger().Error("publish request failed", "requestID", requestID, "requestTopic", requestTopic, "replyTopic", replyTopic, "error", err)
		return nil, fmt.Errorf("publish request: %w", err)
	}

	reply, err := b.Receive(timeoutCtx, replyQueue)
	if err != nil {
		logger().Error("await reply failed", "requestID", requestID, "requestTopic", requestTopic, "replyTopic", replyTopic, "error", err)
		return nil, fmt.Errorf("await reply on %s: %w", replyTopic, err)
	}
	if reply.CorrelationID != "" && reply.CorrelationID != requestID {
		logger().Error("correlation mismatch on reply", "requestID", requestID, "requestTopic", requestTopic, "replyTopic", replyTopic, "got", reply.CorrelationID)
		return nil, fmt.Errorf("await reply on %s: correlation mismatch (got %q, want %q)", replyTopic, reply.CorrelationID, requestID)
	}
	return reply, nil
}

const maxReplyPayloadBytes = 1 << 20

func decodeReplyPayload(payload []byte) (map[string]any, error) {
	if len(payload) == 0 {
		return map[string]any{}, nil
	}
	if len(payload) > maxReplyPayloadBytes {
		return nil, fmt.Errorf("reply payload %d bytes exceeds %d byte cap", len(payload), maxReplyPayloadBytes)
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func mappedToProfile(mapped map[string]any, lookupValue string) (*identity.Profile, error) {
	ensureStringField(mapped, "id", lookupValue)
	ensureStringField(mapped, "workEmail", lookupValue)
	b, err := json.Marshal(mapped)
	if err != nil {
		return nil, err
	}
	var p identity.Profile
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

var canonicalProfileFields = []string{
	"id",
	"displayName",
	"preferredName",
	"workEmail",
	"jobTitle",
	"department",
	"location",
}

func hasProfileEvidence(mapped map[string]any) bool {
	for _, k := range canonicalProfileFields {
		v, ok := mapped[k]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case nil:
			continue
		case string:
			if x != "" {
				return true
			}
		default:
			return true
		}
	}
	return false
}

func detectErrorEnvelope(raw map[string]any) string {
	for _, k := range []string{"error", "errors", "errorMessage", "errorCode"} {
		if v, ok := raw[k]; ok && v != nil && v != "" {
			return fmt.Sprintf("%s=%v", k, v)
		}
	}
	return ""
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func ensureStringField(mapped map[string]any, key, fallback string) {
	v, ok := mapped[key]
	if !ok {
		mapped[key] = fallback
		return
	}
	switch x := v.(type) {
	case nil:
		mapped[key] = fallback
	case string:
		if x == "" {
			mapped[key] = fallback
		}
	case bool, float32, float64, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
		mapped[key] = fmt.Sprint(x)
	default:
		mapped[key] = fallback
	}
}
