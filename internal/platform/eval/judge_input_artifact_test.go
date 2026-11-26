// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artifactmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

var judgeNonceRE = regexp.MustCompile(`\[[0-9a-f]{4}\]`)

func normalizeJudgeNonces(prompt string) string {
	return judgeNonceRE.ReplaceAllString(prompt, "[N]")
}

func TestJudgePromptLayoutWithoutInputArtifactsStaysByteIdentical(t *testing.T) {
	t.Parallel()
	rec := &recordingLLM{content: `{"choice":"A","reasoning":"ok"}`}
	produced := []ArtifactBlock{PrepareTextArtifact("a.md", "text/markdown", []byte("body"))}
	scoreForWithArtifacts(newLLMJudgeEvaluator(), "Q?", "see file", nil, rec, produced, nil, nil)

	const want = "User: Q?\nResponse: see file" +
		"\n\nAgent-produced artifacts:\n\n" +
		"--- Artifact[N]: a.md (text/markdown) ---\nbody\n--- End artifact[N] ---" +
		"\n\nChoose the option that best applies and set the 'choice' field to the corresponding letter:\n" +
		"(A) Yes\n(B) No"
	if got := normalizeJudgeNonces(rec.user); got != want {
		t.Fatalf("judge prompt changed for a trial with no input file:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestJudgePromptLayoutWithInputArtifact(t *testing.T) {
	t.Parallel()
	rec := &recordingLLM{content: `{"choice":"A","reasoning":"ok"}`}
	produced := []ArtifactBlock{PrepareTextArtifact("a.md", "text/markdown", []byte("body"))}
	input := []ArtifactBlock{PrepareTextArtifact("in.csv", "text/csv", []byte("a,b"))}
	scoreForWithArtifacts(newLLMJudgeEvaluator(), "Q?", "see file", nil, rec, produced, input, nil)

	const want = "User: Q?\nResponse: see file" +
		"\n\nInput file provided to the agent (supplied with the user's request — the agent did not create it):\n\n" +
		"--- Artifact[N]: in.csv (text/csv) ---\na,b\n--- End artifact[N] ---" +
		"\n\nAgent-produced artifacts:\n\n" +
		"--- Artifact[N]: a.md (text/markdown) ---\nbody\n--- End artifact[N] ---" +
		"\n\nChoose the option that best applies and set the 'choice' field to the corresponding letter:\n" +
		"(A) Yes\n(B) No"
	if got := normalizeJudgeNonces(rec.user); got != want {
		t.Fatalf("judge prompt with an input file:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestJudgePromptOrdersInputThenExpectedThenTranscriptThenProduced(t *testing.T) {
	t.Parallel()
	rec := &recordingLLM{content: `{"choice":"A","reasoning":"ok"}`}
	ScoreResult(context.Background(), nil, ScoreInputs{
		Evaluator:         newLLMJudgeEvaluator(),
		Prompt:            "Q?",
		Response:          "done",
		Artifacts:         []ArtifactBlock{PrepareTextArtifact("out.md", "text/markdown", []byte("produced-body"))},
		InputArtifacts:    []ArtifactBlock{PrepareTextArtifact("in.csv", "text/csv", []byte("given-body"))},
		ExpectedArtifacts: []ArtifactBlock{PrepareTextArtifact("ref.md", "text/markdown", []byte("reference-body"))},
		Transcript:        "[assistant] working",
	}, rec)

	inputAt := strings.Index(rec.user, inputArtifactsHeader)
	expectedAt := strings.Index(rec.user, expectedArtifactsHeader)
	transcriptAt := strings.Index(rec.user, "Agent execution transcript")
	producedAt := strings.Index(rec.user, producedArtifactsHeader)
	choicesAt := strings.Index(rec.user, "Choose the option that best applies")
	if inputAt < 0 || expectedAt < 0 || transcriptAt < 0 || producedAt < 0 || choicesAt < 0 {
		t.Fatalf("a section is missing (input=%d expected=%d transcript=%d produced=%d choices=%d):\n%s",
			inputAt, expectedAt, transcriptAt, producedAt, choicesAt, rec.user)
	}
	if inputAt >= expectedAt || expectedAt >= transcriptAt || transcriptAt >= producedAt || producedAt >= choicesAt {
		t.Fatalf("sections out of order (input=%d expected=%d transcript=%d produced=%d choices=%d):\n%s",
			inputAt, expectedAt, transcriptAt, producedAt, choicesAt, rec.user)
	}
	givenAt := strings.Index(rec.user, "given-body")
	referenceAt := strings.Index(rec.user, "reference-body")
	producedBodyAt := strings.Index(rec.user, "produced-body")
	if givenAt > referenceAt || referenceAt > producedBodyAt {
		t.Fatalf("file contents rendered under the wrong headers (given=%d reference=%d produced=%d):\n%s",
			givenAt, referenceAt, producedBodyAt, rec.user)
	}
}

func TestJudgePromptLayoutWithExpectedArtifactSaysTheAgentNeverSawIt(t *testing.T) {
	t.Parallel()
	rec := &recordingLLM{content: `{"choice":"A","reasoning":"ok"}`}
	produced := []ArtifactBlock{PrepareTextArtifact("a.md", "text/markdown", []byte("body"))}
	expected := []ArtifactBlock{PrepareTextArtifact("ref.md", "text/markdown", []byte("# right answer"))}
	scoreForWithArtifacts(newLLMJudgeEvaluator(), "Q?", "see file", nil, rec, produced, nil, expected)

	const want = "User: Q?\nResponse: see file" +
		"\n\nExpected file (the reference answer — the agent never saw this):\n\n" +
		"--- Artifact[N]: ref.md (text/markdown) ---\n# right answer\n--- End artifact[N] ---" +
		"\n\nAgent-produced artifacts:\n\n" +
		"--- Artifact[N]: a.md (text/markdown) ---\nbody\n--- End artifact[N] ---" +
		"\n\nChoose the option that best applies and set the 'choice' field to the corresponding letter:\n" +
		"(A) Yes\n(B) No"
	if got := normalizeJudgeNonces(rec.user); got != want {
		t.Fatalf("judge prompt with an expected file:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestExpectedArtifactWithNoProducedFilesReachesTheJudge(t *testing.T) {
	t.Parallel()
	rec := &recordingLLM{content: `{"choice":"B","reasoning":"nothing produced"}`}
	expected := []ArtifactBlock{PrepareTextArtifact("ref.md", "text/markdown", []byte("# right answer"))}
	scoreForWithArtifacts(newLLMJudgeEvaluator(), "Q?", "here you go", nil, rec, nil, nil, expected)

	if !strings.Contains(rec.user, expectedArtifactsHeader) || !strings.Contains(rec.user, "# right answer") {
		t.Fatalf("expected section missing for a trial with no produced files:\n%s", rec.user)
	}
	if strings.Contains(rec.user, producedArtifactsHeader) {
		t.Fatalf("produced section emitted for a trial with no produced files:\n%s", rec.user)
	}
	if !strings.Contains(rec.system, "failure to produce the expected output") {
		t.Fatalf("system prompt does not frame a missing produced artifact as a failure to produce:\n%s", rec.system)
	}
	if !strings.Contains(rec.system, "by CONTENT, not by filename") {
		t.Fatalf("system prompt does not tell the judge to compare by content:\n%s", rec.system)
	}
}

func TestJudgeBudgetDemotesOverBudgetProducedArtifacts(t *testing.T) {
	t.Parallel()
	rec := &recordingLLM{content: `{"choice":"A","reasoning":"ok"}`}
	produced := []ArtifactBlock{textBlockOfRunes("huge.txt", judgeArtifactBudgetRunes+1)}
	ScoreResult(context.Background(), nil, ScoreInputs{
		Evaluator: newLLMJudgeEvaluator(),
		Prompt:    "Q?",
		Response:  "A.",
		Artifacts: produced,
	}, rec)

	if !strings.Contains(rec.user, placeholderNotes[artifactReasonBudgetExceeded]) {
		t.Errorf("over-budget produced block was not demoted to a placeholder:\n%s", rec.user)
	}
	if strings.Contains(rec.user, "--- Artifact[") {
		t.Errorf("over-budget produced block was still inlined:\n%s", rec.user)
	}
	if strings.Contains(rec.user, inputArtifactsHeader) || strings.Contains(rec.user, expectedArtifactsHeader) {
		t.Errorf("a trial with no example files rendered their headings:\n%s", rec.user)
	}
}

func TestJudgePromptRendersBinaryInputAsPlaceholder(t *testing.T) {
	t.Parallel()
	rec := &recordingLLM{content: `{"choice":"A","reasoning":"ok"}`}
	input := []ArtifactBlock{artifactBlockFromBytes("scan.pdf", "application/pdf", []byte("%PDF-1.7\x00\x01binary"))}
	scoreForWithArtifacts(newLLMJudgeEvaluator(), "Q?", "summarised", nil, rec, nil, input, nil)

	const line = "--- Artifact: scan.pdf (application/pdf) — content not included ---"
	headerAt, lineAt := strings.Index(rec.user, inputArtifactsHeader), strings.Index(rec.user, line)
	if headerAt < 0 || lineAt < headerAt {
		t.Fatalf("binary input not rendered under its own header:\n%s", rec.user)
	}
	if regexp.MustCompile(`--- Artifact\[[0-9a-f]{4}\]: scan\.pdf`).MatchString(rec.user) {
		t.Fatalf("binary input must not be inlined in a nonce fence:\n%s", rec.user)
	}
	if strings.Contains(rec.user, "binary") {
		t.Fatalf("binary bytes leaked into the judge prompt:\n%s", rec.user)
	}
	if strings.Contains(rec.user, producedArtifactsHeader) {
		t.Fatalf("produced section emitted for a trial with no produced files:\n%s", rec.user)
	}
}

func TestScoreResultInputArtifactDoesNotSatisfyTheGuard(t *testing.T) {
	t.Parallel()
	rec := &recordingLLM{content: `{"choice":"A","reasoning":"ok"}`}
	input := []ArtifactBlock{PrepareTextArtifact("in.csv", "text/csv", []byte("a,b"))}
	expected := []ArtifactBlock{PrepareTextArtifact("ref.md", "text/markdown", []byte("# right answer"))}
	out := scoreForWithArtifacts(newLLMJudgeEvaluator(), "Q?", "", nil, rec, nil, input, expected)

	if out.Reasoning == nil || *out.Reasoning != "No agent response provided" {
		t.Fatalf("reasoning = %v, want the no-response short circuit", out.Reasoning)
	}
	if out.Score != 0 || out.Passed {
		t.Fatalf("score = %+v, want a failing zero", out)
	}
	if rec.user != "" {
		t.Fatalf("the judge was called for a silent agent:\n%s", rec.user)
	}
}

func TestHeuristicFileOnlyReplyExplainsItself(t *testing.T) {
	t.Parallel()
	files := []ArtifactBlock{PrepareTextArtifact("out.md", "text/markdown", []byte("# 42"))}
	for _, htype := range []string{"valid_json", "json_diff", "levenshtein", "rouge", "mystery"} {
		t.Run(htype, func(t *testing.T) {
			t.Parallel()
			ev := &store.EvalEvaluator{Name: "h", Type: "heuristic", HeuristicType: &htype}
			out := scoreFor(ev, "p", "", ptr("expected text"), nil, files)
			if out.Score != 0 || out.Passed || out.Errored {
				t.Fatalf("grading must stay a plain failing zero: %+v", out)
			}
			if out.Reasoning == nil || *out.Reasoning != reasonHeuristicFileOnlyResponse {
				t.Fatalf("reasoning = %v, want the file-only explanation", out.Reasoning)
			}
		})
	}
}

func TestHeuristicFileOnlyReplyWithEmptyExpected(t *testing.T) {
	t.Parallel()
	files := []ArtifactBlock{PrepareTextArtifact("out.md", "text/markdown", []byte("# 42"))}
	ht := "levenshtein"
	ev := &store.EvalEvaluator{Name: "h", Type: "heuristic", HeuristicType: &ht}
	out := scoreFor(ev, "p", "", ptr(""), nil, files)
	if out.Score != 0 || out.Passed {
		t.Fatalf("an agent that answered only with a file must not score full marks: %+v", out)
	}
}

func TestHeuristicIgnoresInputArtifacts(t *testing.T) {
	t.Parallel()
	ht := "levenshtein"
	ev := &store.EvalEvaluator{Name: "h", Type: "heuristic", HeuristicType: &ht}
	input := []ArtifactBlock{PrepareTextArtifact("in.csv", "text/csv", []byte("a,b"))}

	without := scoreFor(ev, "p", "hello", ptr("hello"), nil, nil)
	with := scoreForWithArtifacts(ev, "p", "hello", ptr("hello"), nil, nil, input, nil)
	if without != with {
		t.Fatalf("input artifacts changed a heuristic grade: without=%+v with=%+v", without, with)
	}
}

func textBlockOfRunes(filename string, runes int) ArtifactBlock {
	return ArtifactJudgeText{Filename: filename, MIMEType: "text/plain", Text: strings.Repeat("a", runes)}
}

const inlined = ""

func assertBlocks(t *testing.T, section string, blocks []ArtifactBlock, want []string) {
	t.Helper()
	if len(blocks) != len(want) {
		t.Fatalf("%s: got %d blocks, want %d", section, len(blocks), len(want))
	}
	for i, b := range blocks {
		switch v := b.(type) {
		case ArtifactJudgeText:
			if want[i] != "" {
				t.Errorf("%s block %d (%s) was inlined, want placeholder %q", section, i, v.Filename, want[i])
			}
		case ArtifactPlaceholder:
			if want[i] == "" {
				t.Errorf("%s block %d (%s) was demoted, want inlined", section, i, v.Filename)
			} else if v.Reason != want[i] {
				t.Errorf("%s block %d reason = %q, want %q", section, i, v.Reason, want[i])
			}
		default:
			t.Errorf("%s block %d has an unexpected type %T", section, i, b)
		}
	}
}

func TestFitArtifactsToJudgeBudget(t *testing.T) {
	t.Parallel()
	const overBudget = judgeArtifactBudgetRunes + 1
	cases := []struct {
		name                      string
		produced, input, expected []ArtifactBlock
		wantProduced              []string
		wantInput                 []string
		wantExpected              []string
	}{
		{
			name:         "both fit",
			produced:     []ArtifactBlock{textBlockOfRunes("p.txt", 1000)},
			input:        []ArtifactBlock{textBlockOfRunes("i.txt", 1000)},
			wantProduced: []string{inlined},
			wantInput:    []string{inlined},
		},
		{
			name:         "produced may spend everything the dataset files do not need",
			produced:     []ArtifactBlock{textBlockOfRunes("p.txt", judgeArtifactBudgetRunes-30)},
			input:        []ArtifactBlock{textBlockOfRunes("i.txt", 10)},
			expected:     []ArtifactBlock{textBlockOfRunes("e.txt", 20)},
			wantProduced: []string{inlined},
			wantInput:    []string{inlined},
			wantExpected: []string{inlined},
		},
		{
			name:         "the input file wins over a produced list that would crowd it out",
			produced:     []ArtifactBlock{textBlockOfRunes("p.txt", judgeArtifactBudgetRunes-9)},
			input:        []ArtifactBlock{textBlockOfRunes("i.txt", 10)},
			wantProduced: []string{artifactReasonBudgetExceeded},
			wantInput:    []string{inlined},
		},
		{
			name:         "an oversize produced list cannot starve the reference",
			produced:     []ArtifactBlock{textBlockOfRunes("huge.txt", judgeArtifactBudgetRunes)},
			expected:     []ArtifactBlock{textBlockOfRunes("e.txt", 60_000)},
			wantProduced: []string{artifactReasonBudgetExceeded},
			wantExpected: []string{inlined},
		},
		{
			name:         "a small later block survives a demoted large one",
			produced:     []ArtifactBlock{textBlockOfRunes("huge.txt", overBudget), textBlockOfRunes("small.txt", 10)},
			wantProduced: []string{artifactReasonBudgetExceeded, inlined},
		},
		{
			name:         "placeholders pass through with their own reason and cost nothing",
			produced:     []ArtifactBlock{FilenameOnlyPlaceholder("logo.png", "image/png", artifactReasonBinary), textBlockOfRunes("p.txt", judgeArtifactBudgetRunes)},
			wantProduced: []string{artifactReasonBinary, inlined},
		},
		{
			name:         "a dataset block too large for the whole budget is demoted, not inlined",
			input:        []ArtifactBlock{textBlockOfRunes("i.txt", overBudget)},
			expected:     []ArtifactBlock{textBlockOfRunes("e.txt", overBudget)},
			wantInput:    []string{artifactReasonBudgetExceeded},
			wantExpected: []string{artifactReasonBudgetExceeded},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotInput, gotExpected, gotProduced := fitArtifactsToJudgeBudget(tc.input, tc.expected, tc.produced)
			assertBlocks(t, "produced", gotProduced, tc.wantProduced)
			assertBlocks(t, "input", gotInput, tc.wantInput)
			assertBlocks(t, "expected", gotExpected, tc.wantExpected)
		})
	}
}

func TestFitArtifactsToJudgeBudgetDoesNotMutateItsInput(t *testing.T) {
	t.Parallel()
	produced := []ArtifactBlock{textBlockOfRunes("huge.txt", judgeArtifactBudgetRunes)}
	input := []ArtifactBlock{textBlockOfRunes("i.txt", judgeArtifactBudgetRunes+1)}
	expected := []ArtifactBlock{textBlockOfRunes("e.txt", judgeArtifactBudgetRunes+1)}

	gotInput, gotExpected, gotProduced := fitArtifactsToJudgeBudget(input, expected, produced)
	if _, ok := gotProduced[0].(ArtifactPlaceholder); !ok {
		t.Fatalf("expected the over-budget produced block to be demoted, got %T", gotProduced[0])
	}
	if _, ok := gotInput[0].(ArtifactPlaceholder); !ok {
		t.Fatalf("expected the over-budget input block to be demoted, got %T", gotInput[0])
	}
	if _, ok := gotExpected[0].(ArtifactPlaceholder); !ok {
		t.Fatalf("expected the over-budget expected block to be demoted, got %T", gotExpected[0])
	}
	if _, ok := produced[0].(ArtifactJudgeText); !ok {
		t.Fatalf("the caller's produced slice was mutated: %T", produced[0])
	}
	if _, ok := input[0].(ArtifactJudgeText); !ok {
		t.Fatalf("the caller's input slice was mutated: %T", input[0])
	}
	if _, ok := expected[0].(ArtifactJudgeText); !ok {
		t.Fatalf("the caller's expected slice was mutated: %T", expected[0])
	}
}

func newExampleBlockFixture(t *testing.T) (*ExecutionService, *artifact.ScopedService, *runState) {
	t.Helper()
	scoped := &artifact.ScopedService{Inner: artifactmem.New(), Scope: "namespace", Namespace: "ns"}
	svc := &ExecutionService{artifacts: scoped, logger: discardLogger()}
	return svc, scoped, &runState{exampleBlocksByRole: map[exampleRoleKey]*exampleBlocksEntry{}}
}

func exampleWithArtifact(t *testing.T, scoped *artifact.ScopedService, role string, body []byte) (*store.EvalRunResult, artifact.Locator) {
	t.Helper()
	return seedExampleArtifact(t, scoped, &store.EvalExampleArtifact{
		ExampleID:   "ex1",
		Role:        role,
		Filename:    "data.csv",
		MIMEType:    "text/csv",
		ContentHash: contentHash(body),
	}, body)
}

func seedExampleArtifact(t *testing.T, scoped *artifact.ScopedService, rec *store.EvalExampleArtifact, body []byte) (*store.EvalRunResult, artifact.Locator) {
	t.Helper()
	rec.SessionID = evalExampleArtifactSessionID(rec.ExampleID, rec.Role, "u")
	example := exampleWithArtifacts(rec.ExampleID, rec)
	loc := scoped.Resolve("", EvalRunnerUserID, rec.SessionID, rec.Filename)
	if _, err := scoped.Inner.Save(context.Background(), loc, body, rec.MIMEType); err != nil {
		t.Fatalf("seed example artifact: %v", err)
	}
	return &store.EvalRunResult{
		ID:                       "r1",
		RunID:                    "run1",
		ExampleID:                example.ID,
		InputArtifactMetadata:    artifactMetadataForExample(example, store.EvalArtifactRoleInput, nil),
		ExpectedArtifactMetadata: artifactMetadataForExample(example, store.EvalArtifactRoleExpected, nil),
	}, loc
}

func inputArtifactExample(t *testing.T, scoped *artifact.ScopedService, body []byte) (*store.EvalRunResult, artifact.Locator) {
	t.Helper()
	return exampleWithArtifact(t, scoped, store.EvalArtifactRoleInput, body)
}

func TestInputArtifactBlocksForJudge(t *testing.T) {
	t.Parallel()
	body := []byte("a,b\n1,2\n")

	t.Run("a text file is inlined", func(t *testing.T) {
		t.Parallel()
		svc, scoped, state := newExampleBlockFixture(t)
		result, _ := inputArtifactExample(t, scoped, body)

		blocks := svc.exampleArtifactBlocksForJudge(context.Background(), result, state, store.EvalArtifactRoleInput)
		if len(blocks) != 1 {
			t.Fatalf("got %d blocks, want 1", len(blocks))
		}
		text, ok := blocks[0].(ArtifactJudgeText)
		if !ok {
			t.Fatalf("got %T, want ArtifactJudgeText", blocks[0])
		}
		if text.Text != string(body) || text.Filename != "data.csv" {
			t.Fatalf("block = %+v, want the seeded bytes", text)
		}
	})

	t.Run("an example with no file yields nothing", func(t *testing.T) {
		t.Parallel()
		svc, _, state := newExampleBlockFixture(t)
		result := &store.EvalRunResult{ID: "r1", RunID: "run1", ExampleID: "ex1"}
		if blocks := svc.exampleArtifactBlocksForJudge(context.Background(), result, state, store.EvalArtifactRoleInput); blocks != nil {
			t.Fatalf("got %v, want nil", blocks)
		}
	})

	t.Run("missing bytes surface as a placeholder, not silence", func(t *testing.T) {
		t.Parallel()
		svc, scoped, state := newExampleBlockFixture(t)
		result, loc := inputArtifactExample(t, scoped, body)
		if err := scoped.Inner.Delete(context.Background(), loc); err != nil {
			t.Fatalf("delete: %v", err)
		}

		blocks := svc.exampleArtifactBlocksForJudge(context.Background(), result, state, store.EvalArtifactRoleInput)
		assertBlocks(t, "input", blocks, []string{artifactReasonExampleUnavailable})
	})

	t.Run("a replace that reuses the version is not graded", func(t *testing.T) {
		t.Parallel()
		svc, scoped, state := newExampleBlockFixture(t)
		result, loc := inputArtifactExample(t, scoped, body)
		if err := scoped.Inner.Delete(context.Background(), loc); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, err := scoped.Inner.Save(context.Background(), loc, []byte("x,y\n9,9\n"), "text/csv"); err != nil {
			t.Fatalf("replace: %v", err)
		}

		blocks := svc.exampleArtifactBlocksForJudge(context.Background(), result, state, store.EvalArtifactRoleInput)
		assertBlocks(t, "input", blocks, []string{artifactReasonExampleReplaced})
	})

	t.Run("a binary file is a placeholder without touching the store", func(t *testing.T) {
		t.Parallel()
		counting := &countingArtifactService{Service: artifactmem.New()}
		scoped := &artifact.ScopedService{Inner: counting, Scope: "namespace", Namespace: "ns"}
		svc := &ExecutionService{artifacts: scoped, logger: discardLogger()}
		state := &runState{exampleBlocksByRole: map[exampleRoleKey]*exampleBlocksEntry{}}
		result, _ := seedExampleArtifact(t, scoped, &store.EvalExampleArtifact{
			ExampleID:   "ex1",
			Role:        store.EvalArtifactRoleInput,
			Filename:    "scan.pdf",
			MIMEType:    "application/pdf",
			ContentHash: contentHash(body),
		}, body)

		blocks := svc.exampleArtifactBlocksForJudge(context.Background(), result, state, store.EvalArtifactRoleInput)
		assertBlocks(t, "input", blocks, []string{artifactReasonBinary})
		if n := counting.loads.Load(); n != 0 {
			t.Fatalf("a binary example file cost %d store loads, want 0", n)
		}
	})

	t.Run("no artifact service surfaces as a placeholder", func(t *testing.T) {
		t.Parallel()
		_, scoped, state := newExampleBlockFixture(t)
		result, _ := inputArtifactExample(t, scoped, body)
		svc := &ExecutionService{logger: discardLogger()}

		blocks := svc.exampleArtifactBlocksForJudge(context.Background(), result, state, store.EvalArtifactRoleInput)
		assertBlocks(t, "input", blocks, []string{artifactReasonExampleUnavailable})
	})
}

func TestExpectedArtifactBlocksForJudge(t *testing.T) {
	t.Parallel()
	inputBody, expectedBody := []byte("a,b\n1,2\n"), []byte("# right answer\n")
	svc, scoped, state := newExampleBlockFixture(t)

	rows := []*store.EvalExampleArtifact{
		{ExampleID: "ex1", Role: store.EvalArtifactRoleInput, Filename: "in.csv", MIMEType: "text/csv", ContentHash: contentHash(inputBody)},
		{ExampleID: "ex1", Role: store.EvalArtifactRoleExpected, Filename: "ref.md", MIMEType: "text/markdown", ContentHash: contentHash(expectedBody)},
	}
	for i, body := range [][]byte{inputBody, expectedBody} {
		rows[i].SessionID = evalExampleArtifactSessionID(rows[i].ExampleID, rows[i].Role, "u")
		loc := scoped.Resolve("", EvalRunnerUserID, rows[i].SessionID, rows[i].Filename)
		if _, err := scoped.Inner.Save(context.Background(), loc, body, rows[i].MIMEType); err != nil {
			t.Fatalf("seed %s artifact: %v", rows[i].Role, err)
		}
	}
	example := exampleWithArtifacts("ex1", rows...)
	result := &store.EvalRunResult{
		ID:                       "r1",
		RunID:                    "run1",
		ExampleID:                example.ID,
		InputArtifactMetadata:    artifactMetadataForExample(example, store.EvalArtifactRoleInput, nil),
		ExpectedArtifactMetadata: artifactMetadataForExample(example, store.EvalArtifactRoleExpected, nil),
	}

	for _, tc := range []struct {
		role string
		want string
	}{
		{store.EvalArtifactRoleExpected, string(expectedBody)},
		{store.EvalArtifactRoleInput, string(inputBody)},
	} {
		blocks := svc.exampleArtifactBlocksForJudge(context.Background(), result, state, tc.role)
		if len(blocks) != 1 {
			t.Fatalf("%s: got %d blocks, want 1", tc.role, len(blocks))
		}
		text, ok := blocks[0].(ArtifactJudgeText)
		if !ok || text.Text != tc.want {
			t.Fatalf("%s role served the wrong file: %+v", tc.role, blocks[0])
		}
	}
}

type countingArtifactService struct {
	artifact.Service
	loads atomic.Int32
}

func (c *countingArtifactService) Load(ctx context.Context, loc artifact.Locator, version *int) ([]byte, string, error) {
	c.loads.Add(1)
	return c.Service.Load(ctx, loc, version)
}

func TestExampleArtifactBlocksLoadOncePerExamplePerRun(t *testing.T) {
	t.Parallel()
	body := []byte("a,b\n1,2\n")

	loadFromAllTrials := func(t *testing.T, breakBytes bool) [][]ArtifactBlock {
		t.Helper()
		counting := &countingArtifactService{Service: artifactmem.New()}
		scoped := &artifact.ScopedService{Inner: counting, Scope: "namespace", Namespace: "ns"}
		svc := &ExecutionService{artifacts: scoped, logger: discardLogger()}
		state := &runState{exampleBlocksByRole: map[exampleRoleKey]*exampleBlocksEntry{}}
		result, loc := inputArtifactExample(t, scoped, body)
		if breakBytes {
			if err := scoped.Inner.Delete(context.Background(), loc); err != nil {
				t.Fatalf("delete: %v", err)
			}
		}

		const trials = 8
		got := make([][]ArtifactBlock, trials)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range trials {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				got[i] = svc.exampleArtifactBlocksForJudge(context.Background(), result, state, store.EvalArtifactRoleInput)
			}()
		}
		close(start)
		wg.Wait()

		if n := counting.loads.Load(); n != 1 {
			t.Fatalf("the store saw %d loads for %d trials, want exactly 1", n, trials)
		}
		return got
	}

	t.Run("a successful load is shared across trials", func(t *testing.T) {
		t.Parallel()
		for trial, blocks := range loadFromAllTrials(t, false) {
			if len(blocks) != 1 {
				t.Fatalf("trial %d: got %d blocks, want 1", trial, len(blocks))
			}
			text, ok := blocks[0].(ArtifactJudgeText)
			if !ok || text.Text != string(body) {
				t.Fatalf("trial %d: block = %+v, want the seeded bytes inlined", trial, blocks[0])
			}
		}
	})

	t.Run("a failed load is memoized, not retried per trial", func(t *testing.T) {
		t.Parallel()
		for _, blocks := range loadFromAllTrials(t, true) {
			assertBlocks(t, "input", blocks, []string{artifactReasonExampleUnavailable})
		}
	})
}
