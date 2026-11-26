// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func outputFactsEvaluator() *store.EvalEvaluator {
	htype := heuristicOutputFacts
	return &store.EvalEvaluator{Name: "Output Facts", Type: evaluatorTypeHeuristic, HeuristicType: &htype}
}

func TestResponseFactsSays(t *testing.T) {
	jsonOut := readResponseFacts(`{"order":{"id":"P-1001","lines":[{"sku":"A1","qty":2}]},"category":"Purchase","refunded":false}`)
	for _, fact := range []string{"P-1001", "purchase", "2", "false", " a1 "} {
		if !jsonOut.says(fact) {
			t.Errorf("JSON output does not say %q", fact)
		}
	}
	text := readResponseFacts("The order  P-1001 is a\nPurchase; no refund issued.")
	for _, fact := range []string{"p-1001 is a purchase", "refund", "purchase"} {
		if !text.says(fact) {
			t.Errorf("text does not say %q", fact)
		}
	}
	for _, fact := range []string{"refun", "P-100", "purchas", "order p", "ssued"} {
		if text.says(fact) {
			t.Errorf("text says %q, which it only contains inside other words", fact)
		}
	}
	cases := []struct {
		response, fact string
		says           bool
	}{
		{`{"approved":false}`, "approved", false},
		{`{"approved":false}`, "false", true},
		{`{"error":null,"result":"ok"}`, "error", false},
		{`{"error":null,"result":"ok"}`, "null", false},
		{`{"n":1e3}`, "1000", true},
		{`{"o":{"q":2}}`, "2", true},
		{`{"tags":["alpha","beta"]}`, "beta", true},
		{`{"msg":"refund\nissued"}`, "refund issued", true},
		{`{"msg":"refund issued today"}`, "refund issued", true},
		{"report.pdf", ".pdf", true},
		{"it costs 50%", "%", true},
		{"price$5", "$5", true},
		{"21°C outside", "°c", true},
		{"costs $5.", "5", true},
		{"apples,pears", "pears", true},
		{"1.5", "5", false},
		{`{"x":-5}`, "5", false},
		{"the total is -5", "5", false},
		{"3.14", "14", false},
		{"order P-1001", "1001", false},
		{"$5.00 total", "5", false},
		{"1,000 units", "000", false},
		{"foo_bar", "foo", false},
		{"refunded; refund issued", "refund", true},
		{"café", "caf", false},
		{"日本語", "日本", true},
		{"東京は大きい", "東京", true},
		{"已退款成功", "退款", true},
		{"คืนเงินแล้ว", "คืนเงิน", true},
		{"これはAPIです", "API", true},
		{"これはAPIsです", "API", false},
		{`{"x":1E3}`, "1000", true},
		{`{"x":1E3}`, "1e3", true},
		{`{"x":5}`, "+5", false},
		{`{"x":5}`, "05", false},
		{`{"x":5}`, "5.", false},
		{`{"x":0.50}`, ".5", false},
		{`{"x":16}`, "0x10", false},
		{`{"x":0.5}`, "1/2", false},
		{`{"x":1e400}`, "10e399", true},
		{`{"x":1e401}`, "10e400", false},
		{`{"x":1` + strings.Repeat("0", 120) + `}`, "1e120", false},
		{`{"x":1` + strings.Repeat("0", 80) + `}`, "1e80", true},
		{"```json\n{\"error\": null}\n```\n", "error", false},
		{"  ```json\n{\"error\": null}\n```  ", "error", false},
		{"  {\"error\": null}  ", "error", false},
		{"```json\n{\"error\": null}", "error", true},
		{"```{\"error\": null}```", "error", true},
		{"Result:\n```json\n{\"error\": null}\n```", "error", true},
		{"{\"error\": null}\n```", "error", true},
		{"abc\n{\"error\": null}\n```", "error", true},
		{"```json\n{\"error\": null}\nabc", "error", true},
		{"サーバーAPIです", "API", true},
		{"ユーザーID", "ID", true},
		{"ユーザーID", "ユーザー", true},
		{"サーバー1が停止", "サーバー", true},
		{"コーヒー1杯", "コーヒー", true},
		{"コンピューター3台", "3", true},
		{"ｻｰﾊﾞｰAPI", "API", true},
		{"भारती", "भारत", false},
		{"कमी है", "कम", false},
		{"भारत है", "भारत", true},
		{"café", "cafe", false},
		{"café au lait", "café", true},
		{"café au lait", "café", true},
		{"가격은 100원", "100", true},
		{"2024년", "2024", true},
		{"API를 호출하세요", "API", true},
		{"ID는 12345입니다", "ID", true},
		{"ID는 12345입니다", "12345", true},
		{"서버ID", "ID", true},
		{"서버는", "서버", false},
		{"한국어", "한국", false},
		{"1.5원", "5", false},
		{"100", "10", false},
		{"ユーザID", "ID", true},
		{"〱API", "API", true},
		{"ほ゛API", "API", true},
		{"5кг", "5", true},
		{"Москва", "Моск", false},
		{"1️⃣ first", "1", true},
		{"OK️", "OK", true},
		{"1⃣ first", "1", true},
		{"İstanbul", "istanbul", true},
		{"İstanbul", "İstanbul", true},
		{"İstanbul", "İstanbul", true},
		{"İ́", "í", true},
		{"foo-東京", "東京", true},
		{"東京-foo", "東京", true},
		{"foo_東京", "東京", true},
		{"東京.pdf", "東京", true},
		{"foo-東京", "foo", false},
		{`{"x":1000}`, "1E3", true},
		{`{"price":19.90}`, " 19.9 ", true},
		{`{"v":"café"}`, "café", true},
		{`{"v":"café"}`, "café", true},
		{"q́x", "q́", false},
		{"ありがとうございます", "ありがとう", true},
		{"コンピュータ", "コンピュ", true},
		{"カタカナ", "カタ", true},
		{"ລາວ", "ລາ", true},
		{"ខ្មែរ", "ខ្មែ", true},
		{"မြန်မာ", "မြန်", true},
		{"```", "x", false},
		{"````", "x", false},
		{"`````", "x", false},
		{"q́ x", "q", false},
		{"q́東京", "q́", true},
		{"東京tokyo", "東京", true},
		{"tokyo東京", "東京", true},
		{"apples,5", "apples", true},
		{"a15", "5", false},
		{"xrefund", "refund", false},
		{"foo-bar", "bar", false},
		{"bar-baz", "bar", false},
		{"apples,5", "5", true},
		{"see -refund flag", "refund", true},
		{`{"price":19.90}`, "19.90", true},
		{`{"price":19.90}`, "19.9", true},
		{`{"price":19.9}`, "19.900", true},
		{`{"n":1.0}`, "1", true},
		{`{"id":9007199254740993}`, "9007199254740993", true},
		{`{"id":9007199254740993}`, "9007199254740992", false},
		{`{"x":1e400}`, "1e400", true},
		{`{"x":1e999999999}`, "1e999999999", true},
		{`{"x":1e999999999}`, "1", false},
		{`{"x":1e300}`, "10e299", true},
		{`{"x":1e500}`, "10e499", false},
		{"```json\n{\"error\": null, \"result\": \"ok\"}\n```", "error", false},
		{"```json\n{\"error\": null, \"result\": \"ok\"}\n```", "ok", true},
		{"```\n{\"error\": null}\n```", "null", false},
		{"Here it is: {\"error\": null}", "error", true},
		{"{\"error\": null} is the reply", "error", true},
		{"```json\n{\"a\": \"x\"}\n```\nand then {\"error\": null}\n```", "error", true},
	}
	for _, tc := range cases {
		if got := readResponseFacts(tc.response).says(tc.fact); got != tc.says {
			t.Errorf("%q says %q = %v, want %v", tc.response, tc.fact, got, tc.says)
		}
	}
	twoValues := readResponseFacts(`{"a":"open","b":"door"}`)
	for _, fact := range []string{"open door", "door open"} {
		if twoValues.says(fact) {
			t.Errorf("%q matched across two JSON values", fact)
		}
	}
}

func TestOutputFactsScore(t *testing.T) {
	response := `{"orderId":"P-1001","category":"return","note":"refund issued"}`
	cases := []struct {
		name       string
		facts      *ExpectedFacts
		wantScore  float64
		wantPassed bool
		wantReason string
	}{
		{"every fact held", &ExpectedFacts{Required: []string{"P-1001", "Return"}, Forbidden: []string{"purchase"}}, 1, true, "3 of 3 facts held."},
		{"broken facts are named", &ExpectedFacts{Required: []string{"P-1001", "purchase"}, Forbidden: []string{"refund"}}, 1.0 / 3, false,
			`1 of 3 facts held. Missing: "purchase". Present but forbidden: "refund".`},
		{"most but not all held", &ExpectedFacts{Required: []string{"P-1001", "return", "purchase"}}, 2.0 / 3, false,
			`2 of 3 facts held. Missing: "purchase".`},
		{"every required held but a forbidden one said", &ExpectedFacts{Required: []string{"P-1001"}, Forbidden: []string{"refund"}}, 0.5, false,
			`1 of 2 facts held. Present but forbidden: "refund".`},
		{"an example with no facts", nil, 0, false, reasonNoExpectedFacts},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := ScoreResult(context.Background(), nil, ScoreInputs{Evaluator: outputFactsEvaluator(), Response: response, ExpectedFacts: tc.facts}, nil)
			if out.Score != tc.wantScore || out.Passed != tc.wantPassed || out.Errored || out.Reasoning == nil || *out.Reasoning != tc.wantReason {
				t.Errorf("got %v %v %v, want %v %v %q", out.Score, out.Passed, out.Reasoning, tc.wantScore, tc.wantPassed, tc.wantReason)
			}
		})
	}
	silent := ScoreResult(context.Background(), nil, ScoreInputs{Evaluator: outputFactsEvaluator(), ExpectedFacts: &ExpectedFacts{Forbidden: []string{"refund"}}}, nil)
	if silent.Passed || silent.Score != 0 {
		t.Errorf("a trial with no response passed on its forbidden facts: %+v", silent)
	}
	unreadable := ScoreResult(context.Background(), nil, ScoreInputs{Evaluator: outputFactsEvaluator(), Response: response, ExpectedFactsUnreadable: true}, nil)
	if !unreadable.Errored || unreadable.Reasoning == nil || !strings.Contains(*unreadable.Reasoning, "expected facts cannot be read") {
		t.Errorf("unreadable stored facts scored %+v, want an errored score naming them", unreadable)
	}
}

func TestReadTrialFactsOnlyForTheOutputFactsEvaluator(t *testing.T) {
	unreadable := `{"required":["a"],"unknown":true}`
	task := &pendingTask{example: &store.EvalExample{ID: "e1", ExpectedFacts: &unreadable}}
	if got := readTrialFacts(slog.New(slog.DiscardHandler), task, []*store.EvalEvaluator{workflowPathEvaluator()}); got != (trialFacts{}) {
		t.Errorf("without an Output Facts evaluator = %+v, want nothing read", got)
	}
	if got := readTrialFacts(slog.New(slog.DiscardHandler), task, []*store.EvalEvaluator{outputFactsEvaluator()}); !got.unreadable {
		t.Errorf("with an Output Facts evaluator = %+v, want the facts read as unreadable", got)
	}
}

func TestValidateExpectedFacts(t *testing.T) {
	if err := validateExpectedFacts(&ExpectedFacts{Required: []string{"a"}, Forbidden: []string{"b"}}); err != nil {
		t.Fatalf("valid facts: %v", err)
	}
	cases := []struct {
		name  string
		facts *ExpectedFacts
		field string
		want  string
	}{
		{"no values", &ExpectedFacts{}, "expected_facts", "needs at least one required or forbidden value"},
		{"a blank value", &ExpectedFacts{Required: []string{" "}}, "expected_facts.required", "must not be blank"},
		{"a value too long", &ExpectedFacts{Forbidden: []string{strings.Repeat("x", maxFactChars+1)}}, "expected_facts.forbidden", fmt.Sprintf("at most %d characters", maxFactChars)},
		{"a value listed twice", &ExpectedFacts{Required: []string{"Refund  issued", "refund issued"}}, "expected_facts.required", `"refund issued" is listed more than once`},
		{"a value listed twice with its accent encoded two ways", &ExpectedFacts{Required: []string{"café", "café"}}, "expected_facts.required", "is listed more than once"},
		{"a value both required and forbidden", &ExpectedFacts{Required: []string{"refund"}, Forbidden: []string{"Refund"}}, "expected_facts.forbidden", `"Refund" is both required and forbidden`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var verr *ValidationError
			if err := validateExpectedFacts(tc.facts); !errors.As(err, &verr) || !strings.Contains(strings.Join(verr.Details[tc.field], "; "), tc.want) {
				t.Errorf("err = %v, want %s naming %q", err, tc.field, tc.want)
			}
		})
	}
	facts := func(required, forbidden int) *ExpectedFacts {
		out := &ExpectedFacts{}
		for i := range required {
			out.Required = append(out.Required, fmt.Sprintf("r%d", i))
		}
		for i := range forbidden {
			out.Forbidden = append(out.Forbidden, fmt.Sprintf("f%d", i))
		}
		return out
	}
	if err := validateExpectedFacts(&ExpectedFacts{Required: []string{strings.Repeat("é", maxFactChars)}}); err != nil {
		t.Errorf("a value of %d two-byte characters refused: %v", maxFactChars, err)
	}
	if err := validateExpectedFacts(&ExpectedFacts{Required: []string{strings.Repeat("é", maxFactChars+1)}}); err == nil {
		t.Errorf("a value of %d characters accepted", maxFactChars+1)
	}
	if err := validateExpectedFacts(facts(50, 50)); err != nil {
		t.Errorf("100 values refused: %v", err)
	}
	if err := validateExpectedFacts(facts(50, 51)); err == nil {
		t.Error("101 values accepted")
	}
	var verr *ValidationError
	blanks := &ExpectedFacts{Required: make([]string, 5000)}
	if err := validateExpectedFacts(blanks); !errors.As(err, &verr) || len(verr.Details) != 1 || len(verr.Details[expectedFactsColumn]) != 1 {
		t.Errorf("5000 blank values reported %v; want only the count refused, so the error does not grow with the request", err)
	}
}

func TestDecodeExpectedFactsRefusesAnUnreadableValue(t *testing.T) {
	want := &ExpectedFacts{Required: []string{"a"}}
	for _, raw := range []string{`not json`, `{"require":["a"]}`, `{"required":["a"]}}`, `{"required":["a"]} {}`, `{}`, `{"required":[" "]}`, `{"required":["a","A"]}`} {
		t.Run(raw, func(t *testing.T) {
			if facts, err := decodeExpectedFacts(&raw); err == nil {
				t.Errorf("decoded %+v, want an error", facts)
			}
			if sameExpectedFacts(&raw, nil) || sameExpectedFacts(&raw, want) {
				t.Error("unreadable stored facts compared equal")
			}
			if shownExpectedFacts(&raw) != nil {
				t.Error("unreadable stored facts were shown")
			}
		})
	}
	stored := `{"required":["a"]}`
	if facts, err := decodeExpectedFacts(&stored); err != nil || !SameExpectedFacts(facts, want) || !sameExpectedFacts(&stored, want) {
		t.Errorf("valid stored facts: %+v, %v", facts, err)
	}
}

func TestExpectedFactsCellNullIsNone(t *testing.T) {
	for _, cell := range []string{"", "  ", "null", " null "} {
		if facts, err := ParseExpectedFactsCell(cell); facts != nil || err != nil {
			t.Errorf("cell %q = %+v, %v; want none", cell, facts, err)
		}
	}
	if _, err := ParseExpectedFactsCell(`{"required":["a"],"banned":["b"]}`); err == nil {
		t.Error("a misspelt key beside a valid one was accepted")
	}
}

func newFactsServices(t *testing.T, workflowEvals bool) (*DatasetService, *ExampleService, string) {
	t.Helper()
	db := store.TestDB(t)
	datasets := NewDatasetService(store.NewEvalDatasetRepo(db), store.NewEvalExampleRepo(db), store.NewEvalRunRepo(db), nil)
	examples := NewExampleService(store.NewEvalExampleRepo(db), store.NewEvalDatasetRepo(db), nil)
	datasets.SetFeatureRegistry(workflowEvalsFlag(workflowEvals))
	examples.SetFeatureRegistry(workflowEvalsFlag(workflowEvals))
	ds, err := datasets.Create(t.Context(), CreateDatasetRequest{Name: "orders"}, "u")
	if err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	return datasets, examples, ds.ID
}

func TestExampleExpectedFactsRoundTrips(t *testing.T) {
	datasets, examples, datasetID := newFactsServices(t, true)
	ctx := t.Context()
	first := &ExpectedFacts{Required: []string{"O-1"}}
	created, err := examples.Create(ctx, datasetID, CreateExampleRequest{Prompt: "refund O-1", ExpectedFacts: first})
	if err != nil || !SameExpectedFacts(created.ExpectedFacts, first) {
		t.Fatalf("create = %+v, %v", created, err)
	}
	stored := func(step string, want *ExpectedFacts) {
		t.Helper()
		got, getErr := examples.Get(ctx, created.ID)
		if getErr != nil || !SameExpectedFacts(got.ExpectedFacts, want) {
			t.Errorf("%s: stored facts %+v, %v; want %+v", step, got, getErr, want)
		}
	}
	stored("create", first)
	if _, keepErr := examples.Update(ctx, created.ID, UpdateExampleRequest{Prompt: new("refund O-2")}); keepErr != nil {
		t.Fatal(keepErr)
	}
	stored("an update leaving them out", first)
	replaced := &ExpectedFacts{Forbidden: []string{"escalated"}}
	if _, replaceErr := examples.Update(ctx, created.ID, UpdateExampleRequest{ExpectedFacts: replaced, ExpectedFactsSet: true}); replaceErr != nil {
		t.Fatal(replaceErr)
	}
	stored("a replacing update", replaced)
	if _, twiceErr := examples.Update(ctx, created.ID, UpdateExampleRequest{ExpectedFacts: &ExpectedFacts{Required: []string{"a", "a"}}, ExpectedFactsSet: true}); twiceErr == nil {
		t.Error("an update with a value listed twice was accepted")
	}
	stored("a refused update", replaced)
	if _, clearErr := examples.Update(ctx, created.ID, UpdateExampleRequest{ExpectedFactsSet: true}); clearErr != nil {
		t.Fatal(clearErr)
	}
	stored("a clearing update", nil)

	csvText := "prompt,expected_facts\n" + `"refund O-3","{""forbidden"":[""refund""]}"` + "\n" + `"no facts",null` + "\n"
	if _, importErr := datasets.ImportExamples(ctx, datasetID, csvText, "text/csv", "u"); importErr != nil {
		t.Fatalf("import: %v", importErr)
	}
	exportedCSV, err := datasets.ExportExamplesCSV(ctx, datasetID)
	if err != nil || !strings.Contains(exportedCSV, "expected_facts") || !strings.Contains(exportedCSV, `""forbidden"":[""refund""]`) {
		t.Errorf("CSV export = %q, %v; want the expected_facts column", exportedCSV, err)
	}
	exportedJSON, err := datasets.ExportExamplesJSON(ctx, datasetID)
	if err != nil || len(exportedJSON) != 3 || exportedJSON[0].ExpectedFacts != nil ||
		!SameExpectedFacts(exportedJSON[1].ExpectedFacts, &ExpectedFacts{Forbidden: []string{"refund"}}) || exportedJSON[2].ExpectedFacts != nil {
		t.Errorf("JSON export = %+v, %v; want the facts on the imported row only", exportedJSON, err)
	}
	bad := "prompt,expected_facts\n" + `"x","{""required"":[""""]}"` + "\n"
	if _, err := datasets.ImportExamples(ctx, datasetID, bad, "text/csv", "u"); err == nil || !strings.Contains(err.Error(), "row 2") {
		t.Errorf("a bad cell imported: %v", err)
	}
}

func TestExpectedFactsAreRefusedWhileWorkflowEvalsIsOff(t *testing.T) {
	datasets, examples, datasetID := newFactsServices(t, false)
	ctx := t.Context()
	facts := &ExpectedFacts{Required: []string{"O-1"}}
	refused := func(what string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "workflow evaluations are turned off") {
			t.Errorf("%s: err = %v, want the workflow_evals refusal", what, err)
		}
	}
	_, err := examples.Create(ctx, datasetID, CreateExampleRequest{Prompt: "x", ExpectedFacts: facts})
	refused("create", err)
	plain, err := examples.Create(ctx, datasetID, CreateExampleRequest{Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = examples.Update(ctx, plain.ID, UpdateExampleRequest{ExpectedFacts: facts, ExpectedFactsSet: true})
	refused("update", err)
	if _, clearErr := examples.Update(ctx, plain.ID, UpdateExampleRequest{ExpectedFactsSet: true}); clearErr != nil {
		t.Errorf("clearing facts: %v", clearErr)
	}
	_, err = datasets.ImportExamples(ctx, datasetID, "prompt,expected_facts\n"+`"x","{""required"":[""a""]}"`+"\n", "text/csv", "u")
	refused("csv import", err)
	_, err = datasets.ImportExamples(ctx, datasetID, `[{"prompt":"x","expectedFacts":{"required":["a"]}}]`, "application/json", "u")
	refused("json import", err)
	_, err = datasets.ImportExamplesStrict(ctx, datasetID, ImportFormatJSON, []byte(`[{"prompt":"x","expected_facts":{"required":["a"]}}]`))
	refused("strict json import", err)
	_, err = datasets.ReconcileExamples(ctx, datasetID, []DesiredExample{{SequenceNumber: 1, Prompt: "x", ExpectedFacts: facts}}, "u")
	refused("reconcile", err)
}

func TestImportedFactsAreReadFromEitherKey(t *testing.T) {
	datasets, examples, datasetID := newFactsServices(t, true)
	ctx := t.Context()
	lenient := `[{"prompt":"lenient","expected_facts":{"required":["a"]}},{"prompt":"none","expectedFacts":null}]`
	if _, err := datasets.ImportExamples(ctx, datasetID, lenient, "application/json", "u"); err != nil {
		t.Fatalf("lenient import: %v", err)
	}
	strict := `[{"prompt":"strict","expected_facts":{"required":["a"]}},{"prompt":"camel","expectedFacts":{"required":["a"]}}]`
	if _, err := datasets.ImportExamplesStrict(ctx, datasetID, ImportFormatJSON, []byte(strict)); err != nil {
		t.Fatalf("strict import: %v", err)
	}
	misspelt := `[{"prompt":"x","expectedFacts":{"required":["a"],"banned":["b"]}}]`
	if _, err := datasets.ImportExamples(ctx, datasetID, misspelt, "application/json", "u"); err == nil {
		t.Error("lenient import: a misspelt key was accepted")
	}
	if _, err := datasets.ImportExamplesStrict(ctx, datasetID, ImportFormatJSON, []byte(misspelt)); err == nil || !strings.Contains(err.Error(), "invalid expectedFacts") {
		t.Errorf("strict import: err = %v, want the row's facts named", err)
	}

	stored, err := examples.List(ctx, datasetID)
	if err != nil || len(stored) != 4 {
		t.Fatalf("stored %d examples, %v; want 4", len(stored), err)
	}
	a := &ExpectedFacts{Required: []string{"a"}}
	want := map[string]*ExpectedFacts{"lenient": a, "none": nil, "strict": a, "camel": a}
	for _, ex := range stored {
		if !SameExpectedFacts(ex.ExpectedFacts, want[ex.Prompt]) {
			t.Errorf("example %q: facts %+v, want %+v", ex.Prompt, ex.ExpectedFacts, want[ex.Prompt])
		}
	}
}

func TestExpectedFactsReconcile(t *testing.T) {
	stored := `{"required":["a"]}`
	actual := []*store.EvalExample{{ID: "e1", SequenceNumber: 1, Prompt: "p", ExpectedFacts: &stored}}
	if same := DiffExamples(actual, []DesiredExample{{SequenceNumber: 1, Prompt: "p", ExpectedFacts: &ExpectedFacts{Required: []string{"a"}}}}); same.Updates() != 0 {
		t.Errorf("equal facts planned %+v, want nothing", same)
	}
	if changed := DiffExamples(actual, []DesiredExample{{SequenceNumber: 1, Prompt: "p", ExpectedFacts: &ExpectedFacts{Forbidden: []string{"a"}}}}); changed.Updates() != 1 {
		t.Errorf("changed facts planned %+v, want one update", changed)
	}
	if sameLength := DiffExamples(actual, []DesiredExample{{SequenceNumber: 1, Prompt: "p", ExpectedFacts: &ExpectedFacts{Required: []string{"b"}}}}); sameLength.Updates() != 1 {
		t.Errorf("a change of the same length planned %+v, want one update", sameLength)
	}
	two := `{"required":["a","b"]}`
	reordered := DiffExamples([]*store.EvalExample{{ID: "e1", SequenceNumber: 1, Prompt: "p", ExpectedFacts: &two}},
		[]DesiredExample{{SequenceNumber: 1, Prompt: "p", ExpectedFacts: &ExpectedFacts{Required: []string{"b", "a"}}}})
	if reordered.Updates() != 1 {
		t.Errorf("reordered facts planned %+v, want one update, since the reasoning lists them in order", reordered)
	}

	datasets, examples, datasetID := newFactsServices(t, true)
	ctx := t.Context()
	reconcile := func(step string, desired []DesiredExample) {
		t.Helper()
		if _, err := datasets.ReconcileExamples(ctx, datasetID, desired, "u"); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
	}
	check := func(step string, want map[int]*ExpectedFacts) {
		t.Helper()
		rows, err := examples.List(ctx, datasetID)
		if err != nil || len(rows) != len(want) {
			t.Fatalf("%s: %d rows, %v; want %d", step, len(rows), err, len(want))
		}
		for _, r := range rows {
			if !SameExpectedFacts(r.ExpectedFacts, want[r.SequenceNumber]) {
				t.Errorf("%s: example %d facts %+v, want %+v", step, r.SequenceNumber, r.ExpectedFacts, want[r.SequenceNumber])
			}
		}
	}
	a, b := &ExpectedFacts{Required: []string{"a"}}, &ExpectedFacts{Forbidden: []string{"b"}}
	reconcile("insert", []DesiredExample{{SequenceNumber: 1, Prompt: "p", ExpectedFacts: a}, {SequenceNumber: 2, Prompt: "q"}})
	check("insert", map[int]*ExpectedFacts{1: a, 2: nil})
	reconcile("change", []DesiredExample{{SequenceNumber: 1, Prompt: "p", ExpectedFacts: b}, {SequenceNumber: 2, Prompt: "q"}})
	check("change", map[int]*ExpectedFacts{1: b, 2: nil})
	reconcile("clear", []DesiredExample{{SequenceNumber: 1, Prompt: "p"}, {SequenceNumber: 2, Prompt: "q"}})
	check("clear", map[int]*ExpectedFacts{1: nil, 2: nil})

	invalid := []DesiredExample{{SequenceNumber: 1, Prompt: "p", ExpectedFacts: &ExpectedFacts{Required: []string{" "}}}}
	if _, err := datasets.ReconcileExamples(ctx, datasetID, invalid, "u"); err == nil {
		t.Fatal("invalid facts were reconciled")
	}
	check("a refused reconcile", map[int]*ExpectedFacts{1: nil, 2: nil})
}

func TestBuildConfigSnapshotCarriesTheExpectedFacts(t *testing.T) {
	stored := `{"required":["a"]}`
	examples := []*store.EvalExample{{ID: "a", SequenceNumber: 1, ExpectedFacts: &stored}, {ID: "b", SequenceNumber: 2}}
	encoded, err := json.Marshal(buildConfigSnapshot(&store.EvalExperiment{ID: "e"}, "d", "orders", examples, nil))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Dataset struct {
			Examples []map[string]json.RawMessage `json:"examples"`
		} `json:"dataset"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := string(decoded.Dataset.Examples[0]["expected_facts"]); got != stored {
		t.Errorf("expected_facts = %s, want %s", got, stored)
	}
	if got, ok := decoded.Dataset.Examples[1]["expected_facts"]; !ok || string(got) != "null" {
		t.Errorf("no-facts example's expected_facts = %s (present %v), want present and null", got, ok)
	}
}

func TestUpdateExampleRequestSendsClearedFactsAsNull(t *testing.T) {
	body, err := json.Marshal(UpdateExampleRequest{ExpectedFactsSet: true})
	if err != nil || !strings.Contains(string(body), `"expectedFacts":null`) {
		t.Errorf("body = %s, %v; want expectedFacts null", body, err)
	}
	body, err = json.Marshal(UpdateExampleRequest{Prompt: new("p")})
	if err != nil || strings.Contains(string(body), "expectedFacts") {
		t.Errorf("body = %s, %v; want no expectedFacts when it is left alone", body, err)
	}
}
