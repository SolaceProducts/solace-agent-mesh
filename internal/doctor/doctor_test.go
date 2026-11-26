// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package doctor

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

type stubCheck struct {
	name        string
	displayName string
	result      CheckResult
	panicMsg    string
}

func (s stubCheck) Name() string        { return s.name }
func (s stubCheck) DisplayName() string { return s.displayName }
func (s stubCheck) Run(_ context.Context, _ Env, _ Context) CheckResult {
	if s.panicMsg != "" {
		panic(s.panicMsg)
	}
	return s.result
}

func TestDoctorRun_HonorsSkipList(t *testing.T) {
	checks := []Check{
		stubCheck{name: "broker_connectivity", displayName: "Broker Connectivity", result: CheckResult{Name: "Broker Connectivity", Status: StatusPass}},
		stubCheck{name: "llm_connectivity", displayName: "LLM Connectivity", result: CheckResult{Name: "LLM Connectivity", Status: StatusPass}},
	}
	d := New(checks, nil)
	results := d.Run(context.Background(), Env{SkipChecks: "Broker_Connectivity"}, ContextLocal)

	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	if results[0].Status != StatusSkip || !strings.Contains(results[0].Reason, "SAM_DOCTOR_SKIP_CHECKS") {
		t.Fatalf("first result: want SKIP via env var, got %+v", results[0])
	}
	if results[1].Status != StatusPass {
		t.Fatalf("second result: want PASS, got %+v", results[1])
	}
}

func TestDoctorRun_RecoverPanicAsFail(t *testing.T) {
	checks := []Check{
		stubCheck{name: "x", displayName: "X", panicMsg: "boom"},
	}
	d := New(checks, nil)
	results := d.Run(context.Background(), Env{}, ContextLocal)

	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Status != StatusFail {
		t.Fatalf("want FAIL, got %s", results[0].Status)
	}
	if !strings.Contains(results[0].Reason, "Unexpected error") || !strings.Contains(results[0].Reason, "boom") {
		t.Fatalf("reason missing panic info: %q", results[0].Reason)
	}
}

func TestHasBlockingFailures(t *testing.T) {
	d := New(nil, nil)
	tests := []struct {
		name string
		in   []CheckResult
		want bool
	}{
		{"empty", nil, false},
		{"all pass", []CheckResult{{Status: StatusPass}}, false},
		{"warn only", []CheckResult{{Status: StatusWarn, Reason: "x"}}, false},
		{"skip only", []CheckResult{{Status: StatusSkip, Reason: "x"}}, false},
		{"one fail", []CheckResult{{Status: StatusPass}, {Status: StatusFail, Reason: "x"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := d.HasBlockingFailures(tt.in); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDetectContext(t *testing.T) {
	tests := []struct {
		raw     string
		want    Context
		wantOK  bool
		wantStr bool
	}{
		{"helm", ContextHelm, true, true},
		{"wheel", ContextWheel, true, true},
		{"local", ContextLocal, true, false},
		{"", "", false, false},
		{"bogus", "", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, ok := DetectContext(Env{DoctorContext: tt.raw})
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("got (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK)
			}
			if got.IsStrict() != tt.wantStr {
				t.Errorf("IsStrict: got %v, want %v", got.IsStrict(), tt.wantStr)
			}
		})
	}
}

func TestParseSkipList(t *testing.T) {
	got := parseSkipList("a, B,, c ")
	want := map[string]struct{}{"a": {}, "b": {}, "c": {}}
	if len(got) != len(want) {
		t.Fatalf("want %d entries, got %d: %v", len(want), len(got), got)
	}
	for k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("missing key %q", k)
		}
	}
}

func TestPrintReport_GroupsByStatus(t *testing.T) {
	d := &Doctor{Out: new(bytes.Buffer)}
	results := []CheckResult{
		{Name: "Pass One", Status: StatusPass, Detail: "ok"},
		{Name: "Fail One", Status: StatusFail, Detail: "x", Reason: "broke"},
		{Name: "Skip One", Status: StatusSkip, Reason: "n/a"},
		{Name: "Warn One", Status: StatusWarn, Reason: "fyi"},
	}
	d.PrintReport(results, ContextLocal)
	out := d.Out.(*bytes.Buffer).String()

	failIdx := strings.Index(out, "Fail One")
	warnIdx := strings.Index(out, "Warn One")
	skipIdx := strings.Index(out, "Skip One")
	passIdx := strings.Index(out, "Pass One")
	if failIdx >= warnIdx || warnIdx >= skipIdx || skipIdx >= passIdx {
		t.Errorf("results not grouped FAIL→WARN→SKIP→PASS:\n%s", out)
	}
	if !strings.Contains(out, "[local]") {
		t.Errorf("header missing context label: %s", out)
	}
}

func TestPrintReport_AllPassedSummary(t *testing.T) {
	d := &Doctor{Out: new(bytes.Buffer)}
	d.PrintReport([]CheckResult{{Name: "X", Status: StatusPass}}, ContextHelm)
	out := d.Out.(*bytes.Buffer).String()
	if !strings.Contains(out, "All checks passed") {
		t.Errorf("expected all-passed banner, got: %s", out)
	}
}

func TestRunCLI_NoContext_DefaultsToLocal(t *testing.T) {
	t.Setenv("SAM_DOCTOR_CONTEXT", "")
	out := new(bytes.Buffer)
	err := RunCLI(context.Background(), CLIOptions{Out: out, ErrOut: new(bytes.Buffer), NoFailOnError: true})
	if err != nil {
		t.Fatalf("want nil err with NoFailOnError, got %v", err)
	}
	if !strings.Contains(out.String(), "defaulting to 'local'") {
		t.Errorf("expected default-to-local notice, got: %s", out.String())
	}
}

func TestRunCLI_InvalidContext_Errors(t *testing.T) {
	t.Setenv("SAM_DOCTOR_CONTEXT", "bogus")
	err := RunCLI(context.Background(), CLIOptions{Out: new(bytes.Buffer), ErrOut: new(bytes.Buffer)})
	if err == nil {
		t.Fatal("want error for unrecognized context, got nil")
	}
	if !strings.Contains(err.Error(), "not recognized") {
		t.Errorf("expected 'not recognized' error, got: %v", err)
	}
}
