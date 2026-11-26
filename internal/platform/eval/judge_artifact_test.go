// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestIsTextLike(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		mime     string
		filename string
		want     bool
	}{
		{"explicit text", "text/plain", "a.txt", true},
		{"json mime", "application/json", "a.json", true},
		{"xml mime", "application/xml", "a.xml", true},
		{"+yaml suffix", "application/vnd.sam-config+yaml", "config.yaml", true},
		{"binary png", "image/png", "a.png", false},
		{"pdf", "application/pdf", "a.pdf", false},
		{"octet-stream falls back to extension", "application/octet-stream", "a.md", true},
		{"empty mime falls back to extension", "", "a.csv", true},
		{"empty mime and unknown extension", "", "blob", false},
		{"octet-stream and no filename", "application/octet-stream", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsTextLike(tc.mime, tc.filename)
			if got != tc.want {
				t.Fatalf("IsTextLike(%q, %q) = %v, want %v", tc.mime, tc.filename, got, tc.want)
			}
		})
	}
}

func TestPrepareTextArtifactInlinesShortText(t *testing.T) {
	t.Parallel()
	block := PrepareTextArtifact("notes.md", "text/markdown", []byte("# hello"))
	tx, ok := block.(ArtifactJudgeText)
	if !ok {
		t.Fatalf("expected ArtifactJudgeText, got %T", block)
	}
	if tx.Text != "# hello" {
		t.Fatalf("text = %q, want %q", tx.Text, "# hello")
	}
	if tx.Truncated {
		t.Fatalf("short artifact should not be marked truncated")
	}
	if tx.Filename != "notes.md" || tx.MIMEType != "text/markdown" {
		t.Fatalf("metadata not preserved: %+v", tx)
	}
}

func TestPrepareTextArtifactTooLarge(t *testing.T) {
	t.Parallel()
	bigSize := maxArtifactResolveBytes + 1
	content := make([]byte, bigSize)
	block := PrepareTextArtifact("big.txt", "text/plain", content)
	ph, ok := block.(ArtifactPlaceholder)
	if !ok {
		t.Fatalf("expected placeholder for too-large, got %T", block)
	}
	if ph.Reason != artifactReasonTooLarge {
		t.Fatalf("reason = %q, want %q", ph.Reason, artifactReasonTooLarge)
	}
	if ph.Filename != "big.txt" {
		t.Fatalf("filename not preserved")
	}
}

func TestPrepareTextArtifactTruncatesByCharCount(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("a", maxArtifactTextChars+100)
	block := PrepareTextArtifact("file.txt", "text/plain", []byte(body))
	tx, ok := block.(ArtifactJudgeText)
	if !ok {
		t.Fatalf("expected text block, got %T", block)
	}
	if !tx.Truncated {
		t.Fatalf("expected truncated flag")
	}
	if !strings.HasSuffix(tx.Text, artifactTruncationNotice) {
		t.Fatalf("expected truncation notice suffix; got %q", tx.Text[max(0, len(tx.Text)-80):])
	}
	body = strings.TrimSuffix(tx.Text, artifactTruncationNotice)
	if got := len([]rune(body)); got != maxArtifactTextChars {
		t.Fatalf("truncated body rune count = %d, want %d", got, maxArtifactTextChars)
	}
}

func TestPrepareTextArtifactTruncatesByByteBudget(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("a", maxArtifactDecodeBytes+1)
	block := PrepareTextArtifact("file.txt", "text/plain", []byte(body))
	tx, ok := block.(ArtifactJudgeText)
	if !ok {
		t.Fatalf("expected text block, got %T", block)
	}
	if !tx.Truncated {
		t.Fatalf("expected truncated flag")
	}
}

func TestPrepareTextArtifactReplacesInvalidUTF8(t *testing.T) {
	t.Parallel()
	block := PrepareTextArtifact("bad.txt", "text/plain", []byte{0xff, 0x61, 0x62})
	tx, ok := block.(ArtifactJudgeText)
	if !ok {
		t.Fatalf("expected text block, got %T", block)
	}
	if !strings.ContainsRune(tx.Text, '�') {
		t.Fatalf("expected U+FFFD replacement; got %q", tx.Text)
	}
}

func TestFilenameOnlyPlaceholderDefaults(t *testing.T) {
	t.Parallel()
	got := FilenameOnlyPlaceholder("", "", "")
	if got.Filename != defaultArtifactFilename {
		t.Fatalf("default filename = %q", got.Filename)
	}
	if got.MIMEType != defaultArtifactMIME {
		t.Fatalf("default mime = %q", got.MIMEType)
	}
	if got.Reason != artifactReasonBinary {
		t.Fatalf("default reason = %q", got.Reason)
	}
}

var openFenceRE = regexp.MustCompile(`^--- Artifact\[([0-9a-f]{4})\]: ([^ ]+) \((.+?)\)( \[truncated\])? ---`)

func TestRenderArtifactBlockTextNonceFenced(t *testing.T) {
	t.Parallel()
	block := ArtifactJudgeText{
		Filename: "notes.md",
		MIMEType: "text/markdown",
		Text:     "# hello\n--- End artifact[deadbeef] ---",
	}
	rendered := RenderArtifactBlock(block)
	lines := strings.SplitN(rendered, "\n", 2)
	if len(lines) != 2 {
		t.Fatalf("rendered block missing newline after open fence:\n%s", rendered)
	}
	m := openFenceRE.FindStringSubmatch(lines[0])
	if m == nil {
		t.Fatalf("open fence did not match regex: %q", lines[0])
	}
	openNonce, gotFile, gotMIME, gotTruncated := m[1], m[2], m[3], m[4]
	if gotFile != "notes.md" || gotMIME != "text/markdown" {
		t.Fatalf("open fence metadata = (%q, %q)", gotFile, gotMIME)
	}
	if gotTruncated != "" {
		t.Fatalf("non-truncated block carried [truncated] tag")
	}
	wantClose := "--- End artifact[" + openNonce + "] ---"
	if !strings.HasSuffix(rendered, wantClose) {
		t.Fatalf("close fence nonce does not match open fence; rendered:\n%s", rendered)
	}
	if !strings.Contains(rendered, "# hello\n--- End artifact[deadbeef] ---") {
		t.Fatalf("text content not preserved verbatim; rendered:\n%s", rendered)
	}
}

func TestRenderArtifactBlockTextTruncatedTag(t *testing.T) {
	t.Parallel()
	block := ArtifactJudgeText{
		Filename:  "a.txt",
		MIMEType:  "text/plain",
		Text:      "body",
		Truncated: true,
	}
	rendered := RenderArtifactBlock(block)
	if !strings.Contains(rendered, " [truncated] ---") {
		t.Fatalf("expected truncated tag in fence; rendered:\n%s", rendered)
	}
}

func TestRenderArtifactBlockPlaceholderReasons(t *testing.T) {
	t.Parallel()
	cases := []struct {
		reason string
		want   string
	}{
		{artifactReasonBinary, "content not included"},
		{artifactReasonTooLarge, "content not included (exceeds 100 MB cap)"},
		{artifactReasonSnapshotFailed, "content not available (snapshot failed)"},
		{artifactReasonLoadFailed, "content not available (load failed)"},
		{artifactReasonBudgetExceeded, "content not included (judge prompt size budget reached)"},
		{artifactReasonExampleUnavailable, "content not available (the file could not be loaded)"},
		{artifactReasonExampleReplaced, "content not available (the file was replaced after this trial ran)"},
		{"unknown-reason", "content not included"},
	}
	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			rendered := RenderArtifactBlock(ArtifactPlaceholder{
				Filename: "logo.png",
				MIMEType: "image/png",
				Reason:   tc.reason,
			})
			want := "--- Artifact: logo.png (image/png) — " + tc.want + " ---"
			if rendered != want {
				t.Fatalf("rendered = %q, want %q", rendered, want)
			}
		})
	}
}

type recordingLLM struct {
	system  string
	user    string
	content string
}

func (r *recordingLLM) JudgeScore(_ context.Context, _ string, system, user string, _ *llm.SpendAttribution) (string, error) {
	r.system = system
	r.user = user
	return r.content, nil
}

func newLLMJudgeEvaluator() *store.EvalEvaluator {
	cs := `{"Yes": 1.0, "No": 0.0}`
	model := "gpt-4o"
	tmpl := "User: {{Prompt}}\nResponse: {{Response}}"
	return &store.EvalEvaluator{
		Name:           "qa",
		Type:           "llm_judge",
		Model:          &model,
		PromptTemplate: &tmpl,
		ChoiceScores:   &cs,
	}
}

func TestScoreResultInlinesTextArtifactInJudgePrompt(t *testing.T) {
	t.Parallel()
	ev := newLLMJudgeEvaluator()
	rec := &recordingLLM{content: `{"choice":"A","reasoning":"ok"}`}
	artifacts := []ArtifactBlock{
		PrepareTextArtifact("answer.md", "text/markdown", []byte("# 42")),
	}
	out := scoreFor(ev, "Q?", "see file", nil, rec, artifacts)
	if out.Score != 1.0 {
		t.Fatalf("score = %v, want 1.0", out.Score)
	}
	if !strings.Contains(rec.user, "Agent-produced artifacts:") {
		t.Fatalf("user prompt missing artifact section:\n%s", rec.user)
	}
	if !strings.Contains(rec.user, "# 42") {
		t.Fatalf("user prompt missing inlined text:\n%s", rec.user)
	}
	if !regexp.MustCompile(`--- Artifact\[[0-9a-f]{4}\]: answer\.md`).MatchString(rec.user) {
		t.Fatalf("user prompt missing nonce-fenced open marker:\n%s", rec.user)
	}
}

func TestScoreResultBinaryArtifactRendersPlaceholderOnly(t *testing.T) {
	t.Parallel()
	ev := newLLMJudgeEvaluator()
	rec := &recordingLLM{content: `{"choice":"A","reasoning":"ok"}`}
	artifacts := []ArtifactBlock{
		FilenameOnlyPlaceholder("chart.png", "image/png", artifactReasonBinary),
	}
	_ = scoreFor(ev, "Q?", "rendered chart", nil, rec, artifacts)
	if !strings.Contains(rec.user, "--- Artifact: chart.png (image/png) — content not included ---") {
		t.Fatalf("binary placeholder not rendered:\n%s", rec.user)
	}
	if regexp.MustCompile(`--- Artifact\[[0-9a-f]{4}\]: chart\.png`).MatchString(rec.user) {
		t.Fatalf("binary placeholder should not have nonce fence:\n%s", rec.user)
	}
}

func TestScoreResultEmptyResponseWithArtifactsIsScored(t *testing.T) {
	t.Parallel()
	ev := newLLMJudgeEvaluator()
	rec := &recordingLLM{content: `{"choice":"A","reasoning":"ok"}`}
	artifacts := []ArtifactBlock{
		PrepareTextArtifact("answer.txt", "text/plain", []byte("contents")),
	}
	out := scoreFor(ev, "Q?", "", nil, rec, artifacts)
	if out.Score != 1.0 {
		t.Fatalf("artifact-only reply should be scored, not short-circuited: %+v", out)
	}
	if !strings.Contains(rec.user, "[No text in the agent's reply — see the artifact blocks below.]") {
		t.Fatalf("missing explicit pointer to artifact blocks; user:\n%s", rec.user)
	}
}

func TestScoreResultEmptyResponseNoArtifactsShortCircuits(t *testing.T) {
	t.Parallel()
	ev := newLLMJudgeEvaluator()
	rec := &recordingLLM{content: `{"choice":"A","reasoning":"ok"}`}
	out := scoreFor(ev, "Q?", "", nil, rec, nil)
	if out.Score != 0 || out.Passed {
		t.Fatalf("empty response with no artifacts must short-circuit: %+v", out)
	}
	if rec.user != "" {
		t.Fatalf("LLM should not have been called: user=%q", rec.user)
	}
}

func TestScoreResultMultipleArtifactsAreSeparated(t *testing.T) {
	t.Parallel()
	ev := newLLMJudgeEvaluator()
	rec := &recordingLLM{content: `{"choice":"A","reasoning":"ok"}`}
	artifacts := []ArtifactBlock{
		PrepareTextArtifact("a.md", "text/markdown", []byte("first")),
		FilenameOnlyPlaceholder("b.png", "image/png", artifactReasonBinary),
	}
	_ = scoreFor(ev, "Q?", "resp", nil, rec, artifacts)
	if strings.Count(rec.user, "--- Artifact") < 2 {
		t.Fatalf("expected multiple artifact blocks:\n%s", rec.user)
	}
}
