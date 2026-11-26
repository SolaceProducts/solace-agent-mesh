// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestChunkText_ShortPassthrough(t *testing.T) {
	got := chunkText("hello", 10)
	if len(got) != 1 || got[0] != "hello" {
		t.Fatalf("expected single chunk, got %v", got)
	}
}

func TestChunkText_Empty(t *testing.T) {
	if got := chunkText("", 10); got != nil {
		t.Fatalf("expected nil for empty input, got %v", got)
	}
	if got := chunkText("abc", 0); got != nil {
		t.Fatalf("expected nil for zero limit, got %v", got)
	}
}

func TestChunkText_ExactLimit(t *testing.T) {
	s := strings.Repeat("a", 10)
	got := chunkText(s, 10)
	if len(got) != 1 || got[0] != s {
		t.Fatalf("expected one exact-limit chunk, got %v", got)
	}
}

func TestChunkText_PrefersNewlineBoundary(t *testing.T) {
	got := chunkText("line one\nline two is here", 12)
	if len(got) < 2 {
		t.Fatalf("expected multiple chunks, got %v", got)
	}
	if got[0] != "line one" {
		t.Fatalf("expected split at newline, got %q", got[0])
	}
}

func TestChunkText_FallsBackToSpaceBoundary(t *testing.T) {
	got := chunkText("alpha beta gamma delta", 12)
	if got[0] != "alpha beta" {
		t.Fatalf("expected split at space, got %q", got[0])
	}
	for _, c := range got {
		if utf8.RuneCountInString(c) > 12 {
			t.Fatalf("chunk exceeds limit: %q", c)
		}
	}
}

func TestChunkText_HardSplitsLongWord(t *testing.T) {
	s := strings.Repeat("x", 25)
	got := chunkText(s, 10)
	if len(got) != 3 {
		t.Fatalf("expected 3 chunks, got %d: %v", len(got), got)
	}
	if strings.Join(got, "") != s {
		t.Fatalf("hard split lost content: %v", got)
	}
}

func TestChunkText_RuneSafe(t *testing.T) {
	s := strings.Repeat("é", 15)
	for _, c := range chunkText(s, 4) {
		if !utf8.ValidString(c) {
			t.Fatalf("invalid UTF-8 chunk: %q", c)
		}
		if utf8.RuneCountInString(c) > 4 {
			t.Fatalf("chunk exceeds rune limit: %q", c)
		}
	}
}

func TestChunkText_PreservesAllContentWords(t *testing.T) {
	s := "The quick brown fox jumps over the lazy dog. " + strings.Repeat("Lorem ipsum dolor sit amet. ", 300)
	chunks := chunkText(s, waTextBodyMax)
	joined := strings.Join(chunks, " ")
	if strings.Join(strings.Fields(joined), " ") != strings.Join(strings.Fields(s), " ") {
		t.Fatal("chunking dropped or reordered words")
	}
	for i, c := range chunks {
		if utf8.RuneCountInString(c) > waTextBodyMax {
			t.Fatalf("chunk %d exceeds %d runes", i, waTextBodyMax)
		}
	}
}

func TestChunkText_AllWhitespace(t *testing.T) {
	if got := chunkText(strings.Repeat(" ", 30), 8); len(got) != 0 {
		t.Fatalf("expected no chunks for all-whitespace input, got %v", got)
	}
}

func replyRig(t *testing.T, m *outboundMock, logger ...*slog.Logger) *Adapter {
	t.Helper()
	log := rigLogger(logger)
	cfg := &WhatsAppConfig{
		AccessToken: "tok", PhoneNumberID: "555111", GraphAPIVersion: "v26.0",
		GraphAPIBaseURL: m.server.URL, MaxMediaSizeMB: 100, DisableRateLimiting: true,
	}
	a := &Adapter{
		core: newArtifactCore(t), cfg: cfg, logger: log,
		claims: newMemoryClaims(), claimLease: defaultClaimLease,
		hilDedup: runtime.NewMapCache(),
		client:   NewClient(cfg, log), ctx: t.Context(),
	}
	startTestSender(t, a)
	m.adapter = a
	return a
}

func deliverMediaBytes(t *testing.T, a *Adapter, taskID, to string, data []byte, mimeType, name string) bool {
	t.Helper()
	target := outboundTarget{
		recipient: to, taskID: taskID,
		userID: "user", agentName: "Builder", sessionID: "sess",
	}
	artSvc := a.core.Services().Artifacts()
	loc := artSvc.Resolve(target.agentName, target.userID, target.sessionID, name)
	if _, err := artSvc.Inner.Save(t.Context(), loc, data, mimeType); err != nil {
		t.Fatalf("save artifact %q: %v", name, err)
	}
	queued := a.queueArtifact(target, name, name, nil, mimeType)
	waitQueueDrained(t, a)
	return queued
}

func TestReply_ShortTextSingleSend(t *testing.T) {
	t.Parallel()
	m := newOutboundMock(t)
	a := replyRig(t, m)

	a.reply(taskT1, "15551230000", "hello there")

	_, msgs := m.snapshot()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 send, got %d", len(msgs))
	}
}

func TestReply_LongTextChunksInOrder(t *testing.T) {
	t.Parallel()
	m := newOutboundMock(t)
	a := replyRig(t, m)

	text := "first " + strings.Repeat("alpha ", 600) + "\nsecond " + strings.Repeat("beta ", 600)
	if n := len(chunkText(text, waTextBodyMax)); n < 2 || n > maxReplyChunks {
		t.Fatalf("fixture must chunk to 2..%d, got %d", maxReplyChunks, n)
	}
	a.reply(taskT1, "15551230000", text)

	uploads, msgs := m.snapshot()
	if uploads != 0 {
		t.Fatalf("chunked path must not upload media, got %d uploads", uploads)
	}
	if len(msgs) < 2 || len(msgs) > maxReplyChunks {
		t.Fatalf("expected 2..%d chunked sends, got %d", maxReplyChunks, len(msgs))
	}
	var joined []string
	for _, msg := range msgs {
		if msg["type"] != "text" {
			t.Fatalf("expected text sends only, got %v", msg["type"])
		}
		body := msg["text"].(map[string]any)["body"].(string)
		if utf8.RuneCountInString(body) > waTextBodyMax {
			t.Fatalf("chunk exceeds %d chars", waTextBodyMax)
		}
		joined = append(joined, body)
	}
	all := strings.Join(joined, " ")
	if !strings.HasPrefix(all, "first alpha") || !strings.Contains(all, "second beta") {
		t.Fatal("chunked sends lost or reordered content")
	}
}

func TestReply_OverflowDeliversPreviewPlusAttachment(t *testing.T) {
	t.Parallel()
	m := newOutboundMock(t)
	a := replyRig(t, m)

	text := strings.Repeat("lorem ipsum dolor sit amet ", 1000)
	if n := len(chunkText(text, waTextBodyMax)); n <= maxReplyChunks {
		t.Fatalf("fixture too small: %d chunks", n)
	}
	a.reply(taskT1, "15551230000", text)

	uploads, msgs := m.snapshot()
	if uploads != 1 {
		t.Fatalf("expected the full text uploaded as one attachment, got %d uploads", uploads)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected preview + document sends, got %d: %v", len(msgs), msgs)
	}
	preview := msgs[0]["text"].(map[string]any)["body"].(string)
	if !strings.HasSuffix(preview, attachmentNote) {
		t.Fatalf("preview must end with the attachment note, got %q", preview[len(preview)-80:])
	}
	if utf8.RuneCountInString(preview) > waTextBodyMax {
		t.Fatal("preview exceeds the message cap")
	}
	if msgs[1]["type"] != "document" {
		t.Fatalf("expected document message, got %v", msgs[1]["type"])
	}
	doc := msgs[1]["document"].(map[string]any)
	if doc["filename"] != "response.txt" {
		t.Fatalf("expected response.txt attachment, got %v", doc["filename"])
	}
}

func TestReply_OverflowAttachmentKeepsBacktickRunsWhole(t *testing.T) {
	t.Parallel()
	m := newOutboundMock(t)
	a := replyRig(t, m)

	text := renderForWhatsApp("~~~md\n```js\nx\n```\n~~~\n" + strings.Repeat("lorem ipsum dolor sit amet ", 1000))
	if !strings.Contains(text, zeroWidthSpace) {
		t.Fatal("fixture must carry a broken backtick run")
	}
	a.reply(taskT1, "15551230000", text)

	m.snapshot()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.uploaded) != 1 {
		t.Fatalf("expected one attachment upload, got %d", len(m.uploaded))
	}
	file := string(m.uploaded[0])
	if strings.Contains(file, zeroWidthSpace) || !strings.Contains(file, "```js") {
		t.Fatalf("attachment should carry the inner fence as written, got %q", file[:min(len(file), 80)])
	}
}
