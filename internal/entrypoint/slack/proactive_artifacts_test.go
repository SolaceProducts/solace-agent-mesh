// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
)

func newProactiveArtifactAdapter(t *testing.T) (*SlackGatewayAdapter, *slackapi.InMemorySlackAPI, *artifact.ScopedService) {
	t.Helper()
	scoped := &artifact.ScopedService{Inner: artmem.New()}
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	rt := runtime.NewRuntime(brk, nil, runtime.WithArtifacts(scoped))
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{})
	core.SetServices(rt.Services())

	mem := slackapi.NewInMemorySlackAPI()
	a := &SlackGatewayAdapter{
		core:   core,
		cfg:    &SlackConfig{},
		api:    mem,
		logger: testLogger(),
		ctx:    t.Context(),
	}
	return a, mem, scoped
}

func proactiveArtifactAddr(t *testing.T) json.RawMessage {
	t.Helper()
	b, _ := json.Marshal(slackProactiveAddress{ChannelID: "C1", UserID: "U1", TeamID: "T1"})
	return b
}

func TestDeliverProactiveArtifacts_UploadsForMatchingOwner(t *testing.T) {
	a, mem, scoped := newProactiveArtifactAdapter(t)
	loc := scoped.Resolve("", "trent@example.org", "sess-1", "funfact.txt")
	if _, err := scoped.Inner.Save(t.Context(), loc, []byte("today's fun fact"), "text/plain"); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}

	a.deliverProactiveArtifacts(a2a.NotifyDeliver{
		RequestID: "r1", UserID: "trent@example.org", ChannelType: "slack",
		Artifacts: []a2a.NotifyArtifact{{UserID: "trent@example.org", SessionID: "sess-1", Filename: "funfact.txt"}},
	}, slackProactiveAddress{ChannelID: "C1", UserID: "U1"})

	if n := countCalls(mem.Calls(), "CompleteUploadExternal"); n != 1 {
		t.Fatalf("expected 1 completed upload, got %d (calls: %+v)", n, mem.Calls())
	}
	for _, m := range mem.Messages() {
		if strings.Contains(m.Text, "Couldn't attach") {
			t.Errorf("a successful upload must not post an undelivered-file notice: %q", m.Text)
		}
	}
}

func TestDeliverProactiveArtifacts_RefusesCrossOwner(t *testing.T) {
	a, mem, scoped := newProactiveArtifactAdapter(t)
	loc := scoped.Resolve("", "victim@example.org", "sess-1", "secret.txt")
	_, _ = scoped.Inner.Save(t.Context(), loc, []byte("secret"), "text/plain")

	a.deliverProactiveArtifacts(a2a.NotifyDeliver{
		RequestID: "r2", UserID: "attacker@example.org", ChannelType: "slack",
		Artifacts: []a2a.NotifyArtifact{{UserID: "victim@example.org", SessionID: "sess-1", Filename: "secret.txt"}},
	}, slackProactiveAddress{ChannelID: "C1", UserID: "U1"})

	if n := countCalls(mem.Calls(), "CompleteUploadExternal"); n != 0 {
		t.Fatalf("cross-owner artifact must NOT be uploaded, got %d uploads", n)
	}
	if n := countCalls(mem.Calls(), "PostMessage"); n != 0 {
		t.Errorf("a deliberate refusal is not a delivery failure; expected no notice, got %d posts", n)
	}
}

func TestDeliverProactiveArtifacts_MissingBytesPostsNotice(t *testing.T) {
	a, mem, _ := newProactiveArtifactAdapter(t)
	a.deliverProactiveArtifacts(a2a.NotifyDeliver{
		RequestID: "r3", UserID: "trent@example.org", ChannelType: "slack",
		Artifacts: []a2a.NotifyArtifact{{UserID: "trent@example.org", SessionID: "sess-1", Filename: "gone.txt"}},
	}, slackProactiveAddress{ChannelID: "C1", UserID: "U1"})

	if n := countCalls(mem.Calls(), "CompleteUploadExternal"); n != 0 {
		t.Fatalf("missing bytes should not upload, got %d", n)
	}
	var noticed bool
	for _, m := range mem.Messages() {
		if strings.Contains(m.Text, "Couldn't attach") && strings.Contains(m.Text, "gone.txt") {
			noticed = true
		}
	}
	if !noticed {
		t.Errorf("expected an undelivered-file notice naming gone.txt, messages: %+v", mem.Messages())
	}
}

func TestDeliverProactiveArtifacts_NoServiceOrEmpty(t *testing.T) {
	a, mem, _ := newProactiveArtifactAdapter(t)
	a.deliverProactiveArtifacts(a2a.NotifyDeliver{RequestID: "r4", UserID: "u1"}, slackProactiveAddress{ChannelID: "C1"})
	if len(mem.Calls()) != 0 {
		t.Errorf("no artifacts → no API calls, got %+v", mem.Calls())
	}
}

func TestDeliverProactiveNotice_TextFailureStillUploadsArtifacts(t *testing.T) {
	a, mem, scoped := newProactiveArtifactAdapter(t)
	loc := scoped.Resolve("", "trent@example.org", "sess-1", "funfact.txt")
	if _, err := scoped.Inner.Save(t.Context(), loc, []byte("fact"), "text/plain"); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}
	mem.SetNextError("PostMessage", errors.New("slack down"))

	a.handleNotifyDeliver(a2a.NotifyDeliver{
		RequestID: "r5", Mode: a2a.NotifyModeNotify, ChannelType: "slack",
		Address: proactiveArtifactAddr(t), Title: "Result", Message: "here is your file",
		UserID:    "trent@example.org",
		Artifacts: []a2a.NotifyArtifact{{UserID: "trent@example.org", SessionID: "sess-1", Filename: "funfact.txt"}},
	})

	if n := countCalls(mem.Calls(), "CompleteUploadExternal"); n != 1 {
		t.Fatalf("a failed text post must not strand the artifact; expected 1 upload, got %d", n)
	}
}
