// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmeshprofile

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func startResponder(t *testing.T, b *broker.DevBroker, topicPattern string, replyPayload any) func() {
	t.Helper()
	q, err := b.CreateQueue("responder-"+topicPattern, broker.QueueOptions{})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if err := b.Subscribe(q, topicPattern); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			msg, err := b.Receive(ctx, q)
			if err != nil {
				return
			}
			var req map[string]any
			if err := json.Unmarshal(msg.Payload, &req); err != nil {
				continue
			}
			replyTopic, _ := req["replyTopic"].(string)
			if replyTopic == "" {
				continue
			}
			body, _ := json.Marshal(replyPayload)
			_ = b.Publish(ctx, replyTopic, &broker.Message{
				Payload:       body,
				CorrelationID: msg.CorrelationID,
			})
		}
	})
	return func() {
		cancel()
		wg.Wait()
		_ = b.DeleteQueue(q.Name())
	}
}

func sampleTestConfig() *Config {
	return &Config{
		BrokerURL:             "tcp://dev-broker:55555",
		BrokerVPN:             "default",
		BrokerUsername:        "test",
		BrokerPassword:        "test",
		RequestTopic:          "test/users/profile/request/v1/{request_id}",
		ResponseTopicPrefix:   "test/users/profile/reply/v1/",
		PayloadKey:            "email",
		RequestTimeoutSeconds: 5,
		FieldMappingConfig:    sampleMappingFixture(),
	}
}

func TestLookup_HappyPath(t *testing.T) {
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = dev.Disconnect(context.Background()) })

	prev := newBroker
	newBroker = func(_ context.Context, _ *Config) (broker.Broker, error) { return dev, nil }
	t.Cleanup(func() { newBroker = prev })

	stop := startResponder(t, dev, "test/users/profile/request/v1/>", sampleReplyFixture())
	defer stop()

	profile, err := NewProvider(sampleTestConfig()).Lookup(t.Context(), "Alice@Example.com")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if profile == nil {
		t.Fatal("Lookup: got nil profile, want canonical Profile")
	}

	if profile.ID != "alice@example.com" {
		t.Errorf("ID: got %q, want %q", profile.ID, "alice@example.com")
	}
	if profile.WorkEmail == "" {
		t.Errorf("WorkEmail empty; expected reply rename or lookup_value fallback")
	}
	if profile.DisplayName != "Alice Example" {
		t.Errorf("DisplayName: got %q, want %q", profile.DisplayName, "Alice Example")
	}
	if got, _ := profile.Extra["country"].(string); got != "United States" {
		t.Errorf("country: got %q, want %q", got, "United States")
	}
	if got, _ := profile.Extra["employeeGroup"].(string); got != "Full Time Employee" {
		t.Errorf("employeeGroup: got %q, want %q", got, "Full Time Employee")
	}
	if profile.Department != "Platform (10000003)" {
		t.Errorf("Department: got %q", profile.Department)
	}
	if profile.Location != "HQ Office (HQ01)" {
		t.Errorf("Location: got %q", profile.Location)
	}
}

func TestLookup_EmptyLookupValue(t *testing.T) {
	t.Parallel()
	p := NewProvider(sampleTestConfig())
	profile, err := p.Lookup(t.Context(), "   ")
	if err != nil {
		t.Errorf("Lookup with empty value: unexpected error %v", err)
	}
	if profile != nil {
		t.Errorf("Lookup with empty value: got profile %+v, want nil", profile)
	}
}

func TestLookup_FallbackIDFromLookupValue(t *testing.T) {
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = dev.Disconnect(context.Background()) })

	prev := newBroker
	newBroker = func(_ context.Context, _ *Config) (broker.Broker, error) { return dev, nil }
	t.Cleanup(func() { newBroker = prev })

	noEmailReply := map[string]any{
		"userFirstName": "Test",
		"userSurname":   "User",
		"country":       "Germany",
	}
	stop := startResponder(t, dev, "test/users/profile/request/v1/>", noEmailReply)
	defer stop()

	profile, err := NewProvider(sampleTestConfig()).Lookup(t.Context(), "test.user@example.com")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if profile == nil {
		t.Fatal("got nil profile")
	}
	if profile.ID != "test.user@example.com" {
		t.Errorf("ID fallback: got %q, want %q", profile.ID, "test.user@example.com")
	}
	if profile.WorkEmail != "test.user@example.com" {
		t.Errorf("WorkEmail fallback: got %q, want %q", profile.WorkEmail, "test.user@example.com")
	}
}

func TestLookup_RequestTopicTemplating(t *testing.T) {
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = dev.Disconnect(context.Background()) })

	prev := newBroker
	newBroker = func(_ context.Context, _ *Config) (broker.Broker, error) { return dev, nil }
	t.Cleanup(func() { newBroker = prev })

	q, err := dev.CreateQueue("topic-sniffer", broker.QueueOptions{})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if err := dev.Subscribe(q, "test/users/profile/request/v1/>"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer func() { _ = dev.DeleteQueue(q.Name()) }()

	captured := make(chan *broker.Message, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		msg, err := dev.Receive(ctx, q)
		if err != nil {
			return
		}
		var req map[string]any
		_ = json.Unmarshal(msg.Payload, &req)
		replyTopic, _ := req["replyTopic"].(string)
		body, _ := json.Marshal(sampleReplyFixture())
		_ = dev.Publish(ctx, replyTopic, &broker.Message{
			Payload:       body,
			CorrelationID: msg.CorrelationID,
		})
		captured <- msg
	}()

	if _, err := NewProvider(sampleTestConfig()).Lookup(t.Context(), "user@example.com"); err != nil {
		t.Fatalf("Lookup: %v", err)
	}

	select {
	case msg := <-captured:
		if !strings.HasPrefix(msg.Topic, "test/users/profile/request/v1/") {
			t.Errorf("request topic: got %q, want prefix test/users/profile/request/v1/", msg.Topic)
		}
		if len(msg.UserProperties) != 0 {
			t.Errorf("UserProperties must be empty; got %v", msg.UserProperties)
		}
		var decoded map[string]any
		if err := json.Unmarshal(msg.Payload, &decoded); err != nil {
			t.Errorf("payload not JSON: %v", err)
		}
		if decoded["email"] != "user@example.com" {
			t.Errorf("payload email: got %v, want %q", decoded["email"], "user@example.com")
		}
		replyTopic, _ := decoded["replyTopic"].(string)
		if !strings.HasPrefix(replyTopic, "test/users/profile/reply/v1/") {
			t.Errorf("payload replyTopic: got %q, want prefix test/users/profile/reply/v1/", replyTopic)
		}
	case <-t.Context().Done():
		t.Fatal("never captured request message")
	}
}

func TestLookup_TimeoutFires(t *testing.T) {
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = dev.Disconnect(context.Background()) })

	prev := newBroker
	newBroker = func(_ context.Context, _ *Config) (broker.Broker, error) { return dev, nil }
	t.Cleanup(func() { newBroker = prev })

	cfg := sampleTestConfig()
	cfg.RequestTimeoutSeconds = 1
	start := time.Now()
	_, err := NewProvider(cfg).Lookup(t.Context(), "alice@example.com")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Lookup: expected timeout error, got nil")
	}
	if elapsed >= 5*time.Second {
		t.Errorf("Lookup elapsed %v — timeout did not fire on schedule; ctx wiring regression?", elapsed)
	}
}

func TestLookup_ErrorEnvelopeRejected(t *testing.T) {
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = dev.Disconnect(context.Background()) })

	prev := newBroker
	newBroker = func(_ context.Context, _ *Config) (broker.Broker, error) { return dev, nil }
	t.Cleanup(func() { newBroker = prev })

	stop := startResponder(t, dev, "test/users/profile/request/v1/>", map[string]any{
		"error": "backend down",
		"code":  503,
	})
	defer stop()

	profile, err := NewProvider(sampleTestConfig()).Lookup(t.Context(), "alice@example.com")
	if err == nil {
		t.Fatalf("expected error for error envelope reply, got profile %+v", profile)
	}
	if profile != nil {
		t.Errorf("expected nil profile on error envelope, got %+v", profile)
	}
	if !strings.Contains(err.Error(), "error envelope") {
		t.Errorf("error message should mention envelope; got %q", err.Error())
	}
}

func TestLookup_NoCanonicalFieldsRejected(t *testing.T) {
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = dev.Disconnect(context.Background()) })

	prev := newBroker
	newBroker = func(_ context.Context, _ *Config) (broker.Broker, error) { return dev, nil }
	t.Cleanup(func() { newBroker = prev })

	stop := startResponder(t, dev, "test/users/profile/request/v1/>", map[string]any{
		"unrelated_field": "value",
	})
	defer stop()

	cfg := sampleTestConfig()
	cfg.FieldMappingConfig = FieldMapping{PassThroughUnmapped: true}
	profile, err := NewProvider(cfg).Lookup(t.Context(), "alice@example.com")
	if err == nil {
		t.Fatalf("expected error for no-canonical-fields reply, got profile %+v", profile)
	}
	if profile != nil {
		t.Errorf("expected nil profile, got %+v", profile)
	}
}

func TestLookup_MalformedReplyRejected(t *testing.T) {
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = dev.Disconnect(context.Background()) })

	prev := newBroker
	newBroker = func(_ context.Context, _ *Config) (broker.Broker, error) { return dev, nil }
	t.Cleanup(func() { newBroker = prev })

	q, err := dev.CreateQueue("malformed-responder", broker.QueueOptions{})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if err = dev.Subscribe(q, "test/users/profile/request/v1/>"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = dev.DeleteQueue(q.Name()) })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		msg, recvErr := dev.Receive(ctx, q)
		if recvErr != nil {
			return
		}
		var req map[string]any
		_ = json.Unmarshal(msg.Payload, &req)
		replyTopic, _ := req["replyTopic"].(string)
		_ = dev.Publish(ctx, replyTopic, &broker.Message{
			Payload:       []byte("not-json-{{{"),
			CorrelationID: msg.CorrelationID,
		})
	}()

	_, err = NewProvider(sampleTestConfig()).Lookup(t.Context(), "alice@example.com")
	if err == nil {
		t.Fatal("expected error for malformed JSON reply")
	}
	if !strings.Contains(err.Error(), "decode reply") {
		t.Errorf("error should mention decode failure; got %q", err.Error())
	}
}

func TestLookup_OversizedReplyRejected(t *testing.T) {
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = dev.Disconnect(context.Background()) })

	prev := newBroker
	newBroker = func(_ context.Context, _ *Config) (broker.Broker, error) { return dev, nil }
	t.Cleanup(func() { newBroker = prev })

	q, err := dev.CreateQueue("oversized-responder", broker.QueueOptions{})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if err = dev.Subscribe(q, "test/users/profile/request/v1/>"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = dev.DeleteQueue(q.Name()) })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		msg, recvErr := dev.Receive(ctx, q)
		if recvErr != nil {
			return
		}
		var req map[string]any
		_ = json.Unmarshal(msg.Payload, &req)
		replyTopic, _ := req["replyTopic"].(string)
		big := make([]byte, maxReplyPayloadBytes+1)
		for i := range big {
			big[i] = 'x'
		}
		_ = dev.Publish(ctx, replyTopic, &broker.Message{
			Payload:       big,
			CorrelationID: msg.CorrelationID,
		})
	}()

	_, err = NewProvider(sampleTestConfig()).Lookup(t.Context(), "alice@example.com")
	if err == nil {
		t.Fatal("expected error for oversized reply")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error should mention size cap; got %q", err.Error())
	}
}

func TestLookup_NumericIDCoerced(t *testing.T) {
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = dev.Disconnect(context.Background()) })

	prev := newBroker
	newBroker = func(_ context.Context, _ *Config) (broker.Broker, error) { return dev, nil }
	t.Cleanup(func() { newBroker = prev })

	stop := startResponder(t, dev, "test/users/profile/request/v1/>", map[string]any{
		"id":        float64(12345),
		"workEmail": "alice@example.com",
	})
	defer stop()

	cfg := sampleTestConfig()
	cfg.FieldMappingConfig = FieldMapping{PassThroughUnmapped: true}
	profile, err := NewProvider(cfg).Lookup(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if profile == nil {
		t.Fatal("expected profile, got nil")
	}
	if profile.ID != "12345" {
		t.Errorf("numeric id: got %q, want %q (lookupValue fallback would be %q)", profile.ID, "12345", "alice@example.com")
	}
}

func TestLookup_CompositeIDFallsBackToLookupValue(t *testing.T) {
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = dev.Disconnect(context.Background()) })

	prev := newBroker
	newBroker = func(_ context.Context, _ *Config) (broker.Broker, error) { return dev, nil }
	t.Cleanup(func() { newBroker = prev })

	stop := startResponder(t, dev, "test/users/profile/request/v1/>", map[string]any{
		"id":        map[string]any{"value": "nested"},
		"workEmail": "alice@example.com",
	})
	defer stop()

	cfg := sampleTestConfig()
	cfg.FieldMappingConfig = FieldMapping{PassThroughUnmapped: true}
	profile, err := NewProvider(cfg).Lookup(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if profile == nil {
		t.Fatal("expected profile, got nil")
	}
	if profile.ID != "alice@example.com" {
		t.Errorf("composite id should fall back to lookupValue; got %q", profile.ID)
	}
}

func TestLookup_EmptyCorrelationIDAccepted(t *testing.T) {
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = dev.Disconnect(context.Background()) })

	prev := newBroker
	newBroker = func(_ context.Context, _ *Config) (broker.Broker, error) { return dev, nil }
	t.Cleanup(func() { newBroker = prev })

	q, err := dev.CreateQueue("no-corr", broker.QueueOptions{})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if err = dev.Subscribe(q, "test/users/profile/request/v1/>"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = dev.DeleteQueue(q.Name()) })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		msg, recvErr := dev.Receive(ctx, q)
		if recvErr != nil {
			return
		}
		var req map[string]any
		_ = json.Unmarshal(msg.Payload, &req)
		replyTopic, _ := req["replyTopic"].(string)
		body, _ := json.Marshal(sampleReplyFixture())
		_ = dev.Publish(ctx, replyTopic, &broker.Message{
			Payload: body,
		})
	}()

	cfg := sampleTestConfig()
	cfg.RequestTimeoutSeconds = 2
	profile, err := NewProvider(cfg).Lookup(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("expected empty-CorrelationID reply to be accepted, got error: %v", err)
	}
	if profile == nil {
		t.Fatal("expected a profile, got nil")
	}
}

func TestLookup_ForeignCorrelationIDRejected(t *testing.T) {
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = dev.Disconnect(context.Background()) })

	prev := newBroker
	newBroker = func(_ context.Context, _ *Config) (broker.Broker, error) { return dev, nil }
	t.Cleanup(func() { newBroker = prev })

	q, err := dev.CreateQueue("bad-corr", broker.QueueOptions{})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if err = dev.Subscribe(q, "test/users/profile/request/v1/>"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = dev.DeleteQueue(q.Name()) })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		msg, recvErr := dev.Receive(ctx, q)
		if recvErr != nil {
			return
		}
		var req map[string]any
		_ = json.Unmarshal(msg.Payload, &req)
		replyTopic, _ := req["replyTopic"].(string)
		body, _ := json.Marshal(sampleReplyFixture())
		_ = dev.Publish(ctx, replyTopic, &broker.Message{
			Payload:       body,
			CorrelationID: "some-other-in-flight-request-id",
		})
	}()

	cfg := sampleTestConfig()
	cfg.RequestTimeoutSeconds = 2
	_, err = NewProvider(cfg).Lookup(t.Context(), "alice@example.com")
	if err == nil {
		t.Fatal("expected correlation-mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "correlation") {
		t.Errorf("error should mention correlation; got %q", err.Error())
	}
}
