// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const fileConsentContentType = "application/vnd.microsoft.teams.card.file.consent"

func countConsentCards(posts []map[string]any) int {
	n := 0
	for _, p := range posts {
		atts, _ := p["attachments"].([]any)
		for _, a := range atts {
			m, _ := a.(map[string]any)
			if m != nil && m["contentType"] == fileConsentContentType {
				n++
			}
		}
	}
	return n
}

func hasNoticeText(posts []map[string]any, substr string) bool {
	for _, p := range posts {
		if t, _ := p["text"].(string); strings.Contains(t, substr) {
			return true
		}
	}
	return false
}

func firstConsentContext(posts []map[string]any) map[string]any {
	for _, p := range posts {
		atts, _ := p["attachments"].([]any)
		for _, a := range atts {
			m, _ := a.(map[string]any)
			if m == nil || m["contentType"] != fileConsentContentType {
				continue
			}
			content, _ := m["content"].(map[string]any)
			if content == nil {
				continue
			}
			ctx, _ := content["acceptContext"].(map[string]any)
			return ctx
		}
	}
	return nil
}

func seedTeamsArtifact(t *testing.T, a *TeamsAdapter, owner, session, filename string) {
	t.Helper()
	loc := a.core.Services().Artifacts().Resolve("", owner, session, filename)
	if _, err := a.core.Services().Artifacts().Inner.Save(a.ctx, loc, []byte("hello"), "text/plain"); err != nil {
		t.Fatalf("seed artifact %s: %v", filename, err)
	}
}

func proactiveMeta(a *TeamsAdapter) *TeamsTaskMeta {
	return &TeamsTaskMeta{ServiceURL: a.botClient.apiBaseURL, ConversationID: "conv-1"}
}

func TestDeliverProactiveArtifacts_OffersConsentCardForMatchingOwner(t *testing.T) {
	var posts []map[string]any
	srv := fakeBotServer(t, &posts, new(int32))
	a, _ := newAdapterWithArtifacts(t, srv)
	seedTeamsArtifact(t, a, "trent@example.org", "sess-1", "funfact.txt")

	a.deliverProactiveArtifacts(a2a.NotifyDeliver{
		RequestID: "r1", UserID: "trent@example.org", ChannelType: "teams",
		Artifacts: []a2a.NotifyArtifact{{UserID: "trent@example.org", SessionID: "sess-1", Filename: "funfact.txt"}},
	}, proactiveMeta(a))

	if n := countConsentCards(posts); n != 1 {
		t.Fatalf("expected 1 file-consent card, got %d (posts: %+v)", n, posts)
	}
	if hasNoticeText(posts, "Couldn't attach") {
		t.Error("a successful offer must not post an undelivered-file notice")
	}
	ctx := firstConsentContext(posts)
	if ctx == nil {
		t.Fatal("consent card carried no acceptContext")
	}
	if ctx["user_id"] != "trent@example.org" || ctx["session_id"] != "sess-1" || ctx["artifact_filename"] != "funfact.txt" {
		t.Errorf("acceptContext missing/incorrect store-backed ref: %+v", ctx)
	}
}

func TestDeliverProactiveArtifacts_MixedBatch(t *testing.T) {
	var posts []map[string]any
	srv := fakeBotServer(t, &posts, new(int32))
	a, _ := newAdapterWithArtifacts(t, srv)
	seedTeamsArtifact(t, a, "trent@example.org", "sess-1", "good.txt")

	a.deliverProactiveArtifacts(a2a.NotifyDeliver{
		RequestID: "r6", UserID: "trent@example.org", ChannelType: "teams",
		Artifacts: []a2a.NotifyArtifact{
			{UserID: "trent@example.org", SessionID: "sess-1", Filename: "good.txt"},
			{UserID: "trent@example.org", SessionID: "sess-1", Filename: "missing.txt"},
		},
	}, proactiveMeta(a))

	if n := countConsentCards(posts); n != 1 {
		t.Fatalf("the loadable artifact should still be offered, got %d cards", n)
	}
	var notice string
	for _, p := range posts {
		if txt, _ := p["text"].(string); strings.Contains(txt, "Couldn't attach") {
			notice = txt
		}
	}
	if notice == "" {
		t.Fatalf("expected an undelivered-file notice, got %+v", posts)
	}
	if !strings.Contains(notice, "missing.txt") || !strings.Contains(notice, "1 file(s)") {
		t.Errorf("notice should name the failed file and count 1, got %q", notice)
	}
	if strings.Contains(notice, "good.txt") {
		t.Errorf("notice must not name the successfully offered file, got %q", notice)
	}
}

func TestDeliverProactiveNotice_PostsTextThenArtifact(t *testing.T) {
	var posts []map[string]any
	srv := fakeBotServer(t, &posts, new(int32))
	a, _ := newAdapterWithArtifacts(t, srv)
	seedTeamsArtifact(t, a, "trent@example.org", "sess-1", "report.txt")

	a.deliverProactiveNotice(a2a.NotifyDeliver{
		RequestID: "r7", Mode: a2a.NotifyModeNotify, ChannelType: "teams",
		Title: "Scheduled task", Message: "here is your report",
		UserID:    "trent@example.org",
		Artifacts: []a2a.NotifyArtifact{{UserID: "trent@example.org", SessionID: "sess-1", Filename: "report.txt"}},
	}, proactiveMeta(a))

	if !hasNoticeText(posts, "here is your report") {
		t.Errorf("expected the notice text to be posted, got %+v", posts)
	}
	if n := countConsentCards(posts); n != 1 {
		t.Errorf("expected the artifact to be offered alongside the text, got %d cards", n)
	}
}

func TestDeliverProactiveArtifacts_RefusesCrossOwner(t *testing.T) {
	var posts []map[string]any
	srv := fakeBotServer(t, &posts, new(int32))
	a, _ := newAdapterWithArtifacts(t, srv)
	seedTeamsArtifact(t, a, "victim@example.org", "sess-1", "secret.txt")

	a.deliverProactiveArtifacts(a2a.NotifyDeliver{
		RequestID: "r2", UserID: "attacker@example.org", ChannelType: "teams",
		Artifacts: []a2a.NotifyArtifact{{UserID: "victim@example.org", SessionID: "sess-1", Filename: "secret.txt"}},
	}, proactiveMeta(a))

	if n := countConsentCards(posts); n != 0 {
		t.Fatalf("cross-owner artifact must NOT be offered, got %d cards", n)
	}
	if len(posts) != 0 {
		t.Errorf("a deliberate refusal must post nothing, got %+v", posts)
	}
}

func TestDeliverProactiveArtifacts_RefusesSharedPrincipal(t *testing.T) {
	var posts []map[string]any
	srv := fakeBotServer(t, &posts, new(int32))
	a, _ := newAdapterWithArtifacts(t, srv)
	seedTeamsArtifact(t, a, rbac.DefaultSystemUserSub, "sess-1", "shared.txt")

	a.deliverProactiveArtifacts(a2a.NotifyDeliver{
		RequestID: "r8", UserID: rbac.DefaultSystemUserSub, ChannelType: "teams",
		Artifacts: []a2a.NotifyArtifact{{UserID: rbac.DefaultSystemUserSub, SessionID: "sess-1", Filename: "shared.txt"}},
	}, proactiveMeta(a))

	if n := countConsentCards(posts); n != 0 {
		t.Fatalf("a shared-principal artifact must not be offered proactively, got %d cards", n)
	}
	if len(posts) != 0 {
		t.Errorf("a deliberate refusal must post nothing, got %+v", posts)
	}
}

func TestDeliverProactiveArtifacts_MissingBytesPostsNotice(t *testing.T) {
	var posts []map[string]any
	srv := fakeBotServer(t, &posts, new(int32))
	a, _ := newAdapterWithArtifacts(t, srv)

	a.deliverProactiveArtifacts(a2a.NotifyDeliver{
		RequestID: "r3", UserID: "trent@example.org", ChannelType: "teams",
		Artifacts: []a2a.NotifyArtifact{{UserID: "trent@example.org", SessionID: "sess-1", Filename: "gone.txt"}},
	}, proactiveMeta(a))

	if n := countConsentCards(posts); n != 0 {
		t.Fatalf("missing bytes must not offer a card, got %d", n)
	}
	if !hasNoticeText(posts, "gone.txt") {
		t.Errorf("expected an undelivered-file notice naming gone.txt, got %+v", posts)
	}
}

func TestDeliverProactiveArtifacts_EmptyAndNoService(t *testing.T) {
	var posts []map[string]any
	srv := fakeBotServer(t, &posts, new(int32))
	a, _ := newAdapterWithArtifacts(t, srv)

	a.deliverProactiveArtifacts(a2a.NotifyDeliver{RequestID: "r4", UserID: "u1"}, proactiveMeta(a))
	a.core.SetServices(&stubServices{arts: nil})
	a.deliverProactiveArtifacts(a2a.NotifyDeliver{
		RequestID: "r5", UserID: "u1",
		Artifacts: []a2a.NotifyArtifact{{UserID: "u1", SessionID: "s", Filename: "x.txt"}},
	}, proactiveMeta(a))

	if len(posts) != 0 {
		t.Errorf("empty list / no service must post nothing, got %+v", posts)
	}
}
