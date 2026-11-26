// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
	goslack "github.com/slack-go/slack"
)

func newHistoryAttachmentTestAdapter(t *testing.T, cfg *SlackConfig) (*SlackGatewayAdapter, *slackapi.InMemorySlackAPI, *artifact.ScopedService) {
	t.Helper()
	api := slackapi.NewInMemorySlackAPI()
	scoped := &artifact.ScopedService{Inner: artmem.New()}

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	rt := runtime.NewRuntime(brk, nil, runtime.WithArtifacts(scoped))
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{})
	core.SetServices(rt.Services())

	a := &SlackGatewayAdapter{
		core:                   core,
		cfg:                    cfg,
		api:                    api,
		emailCache:             NewEmailCache(api, 0, testLogger()),
		historyAttachmentCache: runtime.NewMapCache(),
		logger:                 testLogger(),
		ctx:                    context.Background(),
	}
	return a, api, scoped
}

func seededImageFile(id, name string) goslack.File {
	url := "https://files.slack.com/files-pri/T1/" + id + "/" + name
	return goslack.File{
		ID:                 id,
		Name:               name,
		Mimetype:           "image/png",
		URLPrivateDownload: url,
	}
}

func seededPDFFile(id, name string) goslack.File {
	url := "https://files.slack.com/files-pri/T1/" + id + "/" + name
	return goslack.File{
		ID:                 id,
		Name:               name,
		Mimetype:           "application/pdf",
		URLPrivateDownload: url,
	}
}

func TestCollectHistoryAttachments_AcceptsPDF(t *testing.T) {
	cfg := &SlackConfig{ThreadHistoryLimit: 50, HistoryMaxAttachments: 3}
	a, api, scoped := newHistoryAttachmentTestAdapter(t, cfg)

	f := seededPDFFile("F1", "report.pdf")
	api.SeedFile(f.URLPrivateDownload, []byte("%PDF-1.4 fake contents"))
	api.SeedConversationMessageWithFiles("C1", "100.1", "100.2", "U1", "here's the doc", []goslack.File{f})

	got := a.fetchConversationContext(context.Background(), "C1", "100.1", "", true,
		entrypoint.ArtifactScope{UserID: "u1", SessionID: "s1"})

	if !strings.Contains(got, `[shared "report.pdf"`) || !strings.Contains(got, "F1_report.pdf") {
		t.Errorf("application/pdf must be accepted by the MIME filter, same as images; got:\n%s", got)
	}

	loc := scoped.Resolve("", "u1", "s1", "F1_report.pdf")
	content, mimeType, err := scoped.Inner.Load(context.Background(), loc, nil)
	if err != nil {
		t.Fatalf("load saved artifact: %v", err)
	}
	if string(content) != "%PDF-1.4 fake contents" {
		t.Errorf("artifact content = %q, want the seeded PDF bytes", content)
	}
	if mimeType != "application/pdf" {
		t.Errorf("artifact mimeType = %q, want application/pdf", mimeType)
	}
}

func TestFetchConversationContext_AnsweredMessageFilesAreNotCollectedAsHistory(t *testing.T) {
	scope := entrypoint.ArtifactScope{UserID: "u1", SessionID: "s1"}
	for _, tc := range []struct {
		name         string
		answeredText string
	}{
		{"answered message with text", "see attached"},
		{"file-only answered message", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &SlackConfig{ThreadHistoryLimit: 50, HistoryMaxAttachments: 3}
			a, api, scoped := newHistoryAttachmentTestAdapter(t, cfg)

			earlier := seededImageFile("F1", "earlier.png")
			answered := seededImageFile("F2", "answered.png")
			api.SeedFile(earlier.URLPrivateDownload, []byte("earlier-bytes"))
			api.SeedFile(answered.URLPrivateDownload, []byte("answered-bytes"))
			api.SeedConversationMessage("C1", "100.1", "100.1", "U1", "thread root")
			api.SeedConversationMessageWithFiles("C1", "100.1", "100.2", "U1", "an earlier file", []goslack.File{earlier})
			api.SeedConversationMessageWithFiles("C1", "100.1", "100.3", "U1", tc.answeredText, []goslack.File{answered})

			got := a.fetchConversationContext(context.Background(), "C1", "100.1", "100.3", true, scope)

			var earlierLine string
			for line := range strings.SplitSeq(got, "\n") {
				if strings.Contains(line, "an earlier file") {
					earlierLine = line
				}
			}
			if !strings.Contains(earlierLine, "F1_earlier.png") {
				t.Errorf("the earlier message's file should be referenced on its own line, got line %q in:\n%s", earlierLine, got)
			}
			if strings.Contains(got, "F2_") || strings.Contains(got, "answered.png") {
				t.Errorf("the answered message's file must not be referenced in the context:\n%s", got)
			}

			saved := func(filename string) string {
				content, _, err := scoped.Inner.Load(context.Background(), scoped.Resolve("", "u1", "s1", filename), nil)
				if err != nil {
					t.Fatalf("load %s: %v", filename, err)
				}
				return string(content)
			}
			if got := saved("F1_earlier.png"); got != "earlier-bytes" {
				t.Errorf("the earlier message's file should be saved as a history artifact, got content %q", got)
			}
			if got := saved("F2_answered.png"); got != "" {
				t.Errorf("the answered message's file was saved as a history artifact, content %q", got)
			}
		})
	}
}

func TestCollectHistoryAttachments_CapsAtMostRecent(t *testing.T) {
	cfg := &SlackConfig{ThreadHistoryLimit: 50, HistoryMaxAttachments: 3}
	a, api, scoped := newHistoryAttachmentTestAdapter(t, cfg)

	for i := range 5 {
		id := fmt.Sprintf("F%d", i)
		f := seededImageFile(id, "image.png")
		api.SeedFile(f.URLPrivateDownload, []byte(fmt.Sprintf("content-%d", i)))
		api.SeedConversationMessageWithFiles("C1", "100.1", fmt.Sprintf("100.%d", i+1), "U1", "", []goslack.File{f})
	}

	got := a.fetchConversationContext(context.Background(), "C1", "100.1", "", true,
		entrypoint.ArtifactScope{UserID: "u1", SessionID: "s1"})

	if n := strings.Count(got, "[shared"); n != 3 {
		t.Fatalf("attachment references = %d, want 3 (capped by HistoryMaxAttachments); got:\n%s", n, got)
	}
	for _, id := range []string{"F2", "F3", "F4"} {
		if !strings.Contains(got, id+"_image.png") {
			t.Errorf("expected most-recent file %s to be attached; got:\n%s", id, got)
		}
	}
	for _, id := range []string{"F0", "F1"} {
		if strings.Contains(got, id+"_image.png") {
			t.Errorf("did not expect oldest file %s to be attached (over cap); got:\n%s", id, got)
		}
	}

	loc := scoped.Resolve("", "u1", "s1", "F4_image.png")
	content, _, err := scoped.Inner.Load(context.Background(), loc, nil)
	if err != nil {
		t.Fatalf("load saved artifact: %v", err)
	}
	if string(content) != "content-4" {
		t.Errorf("artifact content = %q, want %q", content, "content-4")
	}
}

func TestCollectHistoryAttachments_FiltersNonImagePDFMime(t *testing.T) {
	cfg := &SlackConfig{ThreadHistoryLimit: 50, HistoryMaxAttachments: 3}
	a, api, _ := newHistoryAttachmentTestAdapter(t, cfg)

	f := goslack.File{
		ID:                 "F1",
		Name:               "data.csv",
		Mimetype:           "text/csv",
		URLPrivateDownload: "https://files.slack.com/files-pri/T1/F1/data.csv",
	}
	api.SeedFile(f.URLPrivateDownload, []byte("a,b,c"))
	api.SeedConversationMessageWithFiles("C1", "100.1", "100.2", "U1", "shared a spreadsheet", []goslack.File{f})

	got := a.fetchConversationContext(context.Background(), "C1", "100.1", "", true,
		entrypoint.ArtifactScope{UserID: "u1", SessionID: "s1"})

	if strings.Contains(got, "[shared") {
		t.Errorf("non-image/PDF file should not be downloaded/attached; got:\n%s", got)
	}
	if !strings.Contains(got, "shared a spreadsheet") {
		t.Errorf("message text should still appear; got:\n%s", got)
	}
	for _, call := range api.Calls() {
		if call.Method == "GetFile" {
			t.Errorf("GetFile should not be called for a filtered-out MIME type, got call: %+v", call)
		}
	}
}

func TestCollectHistoryAttachments_DedupesAcrossCalls(t *testing.T) {
	cfg := &SlackConfig{ThreadHistoryLimit: 50, HistoryMaxAttachments: 3}
	a, api, _ := newHistoryAttachmentTestAdapter(t, cfg)

	f := seededImageFile("F1", "cat.png")
	api.SeedFile(f.URLPrivateDownload, []byte("cat-bytes"))
	api.SeedConversationMessageWithFiles("C1", "100.1", "100.2", "U1", "", []goslack.File{f})

	scope := entrypoint.ArtifactScope{UserID: "u1", SessionID: "s1"}
	first := a.fetchConversationContext(context.Background(), "C1", "100.1", "", true, scope)
	second := a.fetchConversationContext(context.Background(), "C1", "100.1", "", true, scope)

	if !strings.Contains(first, "F1_cat.png") || !strings.Contains(second, "F1_cat.png") {
		t.Fatalf("expected both fetches to reference the same file; first=%q second=%q", first, second)
	}

	getFileCalls := 0
	for _, call := range api.Calls() {
		if call.Method == "GetFile" {
			getFileCalls++
		}
	}
	if getFileCalls != 1 {
		t.Errorf("GetFile calls = %d, want 1 (second fetch should hit historyAttachmentCache)", getFileCalls)
	}
}

func TestFetchConversationContext_ImageOnlyMessageNotDropped(t *testing.T) {
	cfg := &SlackConfig{ThreadHistoryLimit: 50, HistoryMaxAttachments: 3}
	a, api, _ := newHistoryAttachmentTestAdapter(t, cfg)

	f := seededImageFile("F1", "screenshot.png")
	api.SeedFile(f.URLPrivateDownload, []byte("screenshot-bytes"))
	api.SeedConversationMessageWithFiles("C1", "100.1", "100.2", "U1", "", []goslack.File{f})

	got := a.fetchConversationContext(context.Background(), "C1", "100.1", "", true,
		entrypoint.ArtifactScope{UserID: "u1", SessionID: "s1"})

	if got == "" {
		t.Fatal("expected non-empty context: an image-only historical message must not be silently dropped")
	}
	if !strings.Contains(got, `[shared "screenshot.png"`) || !strings.Contains(got, "F1_screenshot.png") {
		t.Errorf("expected an attachment reference (display name + loadable artifact filename) for the image-only message; got:\n%s", got)
	}
}

func TestCollectHistoryAttachments_DisabledByDefault(t *testing.T) {
	cfg := &SlackConfig{ThreadHistoryLimit: 50}
	a, api, _ := newHistoryAttachmentTestAdapter(t, cfg)

	f := seededImageFile("F1", "cat.png")
	api.SeedFile(f.URLPrivateDownload, []byte("cat-bytes"))
	api.SeedConversationMessageWithFiles("C1", "100.1", "100.2", "U1", "look at this", []goslack.File{f})

	got := a.fetchConversationContext(context.Background(), "C1", "100.1", "", true,
		entrypoint.ArtifactScope{UserID: "u1", SessionID: "s1"})

	if strings.Contains(got, "[shared") {
		t.Errorf("HistoryMaxAttachments=0 must disable attachment collection entirely; got:\n%s", got)
	}
	if !strings.Contains(got, "look at this") {
		t.Errorf("message text should still appear; got:\n%s", got)
	}
}

func TestCollectHistoryAttachments_SkipsFailedDownloadGracefully(t *testing.T) {
	cfg := &SlackConfig{ThreadHistoryLimit: 50, HistoryMaxAttachments: 2}
	a, api, _ := newHistoryAttachmentTestAdapter(t, cfg)

	older := seededImageFile("F1", "old.png")
	newer := seededImageFile("F2", "new.png")
	api.SeedFile(older.URLPrivateDownload, []byte("old-bytes"))
	api.SeedFile(newer.URLPrivateDownload, []byte("new-bytes"))
	api.SeedConversationMessageWithFiles("C1", "100.1", "100.2", "U1", "first image", []goslack.File{older})
	api.SeedConversationMessageWithFiles("C1", "100.1", "100.3", "U1", "second image", []goslack.File{newer})

	api.SetNextError("GetFile", fmt.Errorf("slack_api_error: file_deleted"))

	got := a.fetchConversationContext(context.Background(), "C1", "100.1", "", true,
		entrypoint.ArtifactScope{UserID: "u1", SessionID: "s1"})

	if got == "" {
		t.Fatal("a single failed historical download must not blank out the whole conversation context")
	}
	if !strings.Contains(got, "first image") || !strings.Contains(got, "second image") {
		t.Errorf("both messages' text must still render even though one attachment failed; got:\n%s", got)
	}
	if n := strings.Count(got, "[shared"); n != 1 {
		t.Errorf("attachment references = %d, want 1 (only the older file, since the newer one's download failed); got:\n%s", n, got)
	}
	if strings.Contains(got, "F2_new.png") {
		t.Errorf("the failed file must not appear as a loadable reference; got:\n%s", got)
	}
	if !strings.Contains(got, "F1_old.png") {
		t.Errorf("the older file should still succeed once the attempt budget reaches it; got:\n%s", got)
	}
}

func TestCollectHistoryAttachments_ChannelHistoryBranch(t *testing.T) {
	cfg := &SlackConfig{ChannelHistoryLimit: 30, HistoryMaxAttachments: 3}
	a, api, _ := newHistoryAttachmentTestAdapter(t, cfg)

	f := seededImageFile("F1", "banner.png")
	api.SeedFile(f.URLPrivateDownload, []byte("banner-bytes"))
	api.SeedConversationMessageWithFiles("C1", "", "100.1", "U1", "check this out", []goslack.File{f})

	got := a.fetchConversationContext(context.Background(), "C1", "", "", false,
		entrypoint.ArtifactScope{UserID: "u1", SessionID: "s1"})

	if !strings.Contains(got, `[shared "banner.png"`) || !strings.Contains(got, "F1_banner.png") {
		t.Errorf("HistoryMaxAttachments must apply to the channel-history branch too, not just thread history; got:\n%s", got)
	}
}

func TestCollectHistoryAttachments_CapBreaksMidMessage(t *testing.T) {
	cfg := &SlackConfig{ThreadHistoryLimit: 50, HistoryMaxAttachments: 3}
	a, api, _ := newHistoryAttachmentTestAdapter(t, cfg)

	older1, older2 := seededImageFile("F1", "a.png"), seededImageFile("F2", "b.png")
	newer1, newer2 := seededImageFile("F3", "c.png"), seededImageFile("F4", "d.png")
	for _, f := range []goslack.File{older1, older2, newer1, newer2} {
		api.SeedFile(f.URLPrivateDownload, []byte("bytes-"+f.ID))
	}
	api.SeedConversationMessageWithFiles("C1", "100.1", "100.2", "U1", "", []goslack.File{older1, older2})
	api.SeedConversationMessageWithFiles("C1", "100.1", "100.3", "U1", "", []goslack.File{newer1, newer2})

	got := a.fetchConversationContext(context.Background(), "C1", "100.1", "", true,
		entrypoint.ArtifactScope{UserID: "u1", SessionID: "s1"})

	for _, id := range []string{"F3", "F4", "F1"} {
		if !strings.Contains(got, id+"_") {
			t.Errorf("expected %s to be attached; got:\n%s", id, got)
		}
	}
	if strings.Contains(got, "F2_") {
		t.Errorf("F2 is the 4th file scanned and must be excluded by the cap; got:\n%s", got)
	}
}

func TestCollectHistoryAttachments_CrossScopeDoesNotReuseOtherScopesArtifact(t *testing.T) {
	cfg := &SlackConfig{ThreadHistoryLimit: 50, HistoryMaxAttachments: 3}
	a, api, scoped := newHistoryAttachmentTestAdapter(t, cfg)

	f := seededImageFile("F1", "shared.png")
	api.SeedFile(f.URLPrivateDownload, []byte("shared-bytes"))
	api.SeedConversationMessageWithFiles("C1", "100.1", "100.2", "U1", "", []goslack.File{f})

	scopeA := entrypoint.ArtifactScope{UserID: "userA", SessionID: "sessionA"}
	scopeB := entrypoint.ArtifactScope{UserID: "userB", SessionID: "sessionB"}

	gotA := a.fetchConversationContext(context.Background(), "C1", "100.1", "", true, scopeA)
	gotB := a.fetchConversationContext(context.Background(), "C1", "100.1", "", true, scopeB)

	if !strings.Contains(gotA, "F1_shared.png") || !strings.Contains(gotB, "F1_shared.png") {
		t.Fatalf("both scopes should get a reference; A=%q B=%q", gotA, gotB)
	}

	for _, s := range []struct {
		name  string
		scope entrypoint.ArtifactScope
	}{{"A", scopeA}, {"B", scopeB}} {
		loc := scoped.Resolve("", s.scope.UserID, s.scope.SessionID, "F1_shared.png")
		content, _, err := scoped.Inner.Load(context.Background(), loc, nil)
		if err != nil {
			t.Errorf("scope %s: artifact not independently saved/loadable: %v", s.name, err)
			continue
		}
		if string(content) != "shared-bytes" {
			t.Errorf("scope %s: artifact content = %q, want %q", s.name, content, "shared-bytes")
		}
	}

	getFileCalls := 0
	for _, call := range api.Calls() {
		if call.Method == "GetFile" {
			getFileCalls++
		}
	}
	if getFileCalls != 2 {
		t.Errorf("GetFile calls = %d, want 2 (one per distinct scope — a cache hit across scopes would wrongly leave this at 1)", getFileCalls)
	}
}

func TestResolveHistoryAttachment_ConcurrentCallsDoNotDuplicateSave(t *testing.T) {
	cfg := &SlackConfig{ThreadHistoryLimit: 50, HistoryMaxAttachments: 3}
	a, api, _ := newHistoryAttachmentTestAdapter(t, cfg)

	f := seededImageFile("F1", "race.png")
	api.SeedFile(f.URLPrivateDownload, []byte("race-bytes"))
	scope := entrypoint.ArtifactScope{UserID: "u1", SessionID: "s1"}
	artSvc := a.core.Services().Artifacts()

	const n = 20
	var wg sync.WaitGroup
	results := make([]bool, n)
	for i := range n {
		wg.Go(func() {
			_, ok := a.resolveHistoryAttachment(context.Background(), f, artSvc, scope, "C1", "100.1")
			results[i] = ok
		})
	}
	wg.Wait()

	succeeded := 0
	for _, ok := range results {
		if ok {
			succeeded++
		}
	}
	if succeeded == 0 {
		t.Fatal("expected at least one of the concurrent callers to succeed")
	}

	getFileCalls := 0
	for _, call := range api.Calls() {
		if call.Method == "GetFile" {
			getFileCalls++
		}
	}
	if getFileCalls != 1 {
		t.Errorf("GetFile calls = %d, want exactly 1 across %d concurrent callers for the same (scope, fileID) — a TOCTOU race would duplicate this", getFileCalls, n)
	}
}

func TestResolveHistoryAttachment_RefetchesAfterArtifactDeleted(t *testing.T) {
	cfg := &SlackConfig{ThreadHistoryLimit: 50, HistoryMaxAttachments: 3}
	a, api, scoped := newHistoryAttachmentTestAdapter(t, cfg)

	f := seededImageFile("F1", "deleteme.png")
	api.SeedFile(f.URLPrivateDownload, []byte("v1-bytes"))
	scope := entrypoint.ArtifactScope{UserID: "u1", SessionID: "s1"}
	artSvc := a.core.Services().Artifacts()

	att, ok := a.resolveHistoryAttachment(context.Background(), f, artSvc, scope, "C1", "100.1")
	if !ok {
		t.Fatalf("first resolve should succeed")
	}

	loc := scoped.Resolve("", "u1", "s1", att.artifactFilename)
	if err := scoped.Inner.Delete(context.Background(), loc); err != nil {
		t.Fatalf("delete artifact: %v", err)
	}

	if _, ok := a.resolveHistoryAttachment(context.Background(), f, artSvc, scope, "C1", "100.1"); !ok {
		t.Fatalf("resolve after deletion should re-save, not fail")
	}
	if _, _, err := scoped.Inner.Load(context.Background(), loc, nil); err != nil {
		t.Errorf("artifact should be loadable again after the deletion is detected and re-saved: %v", err)
	}

	getFileCalls := 0
	for _, call := range api.Calls() {
		if call.Method == "GetFile" {
			getFileCalls++
		}
	}
	if getFileCalls != 2 {
		t.Errorf("GetFile calls = %d, want 2 (initial save + re-save after the deletion is detected)", getFileCalls)
	}
}
