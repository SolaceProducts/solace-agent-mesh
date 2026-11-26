// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

func newTestResolver(arts *artifact.ScopedService) *EmbedResolver {
	return NewEmbedResolver(arts, "test-agent", "user1", "sess1", "", slog.Default())
}

func TestResolveMathBasicArithmetic(t *testing.T) {
	r := newTestResolver(nil)
	ctx := context.Background()

	tests := []struct {
		expr string
		want string
	}{
		{"2 + 2", "4"},
		{"10 - 3", "7"},
		{"6 * 7", "42"},
		{"100 / 4", "25.0"},
		{"2 ** 3", "8.0"},
		{"10 % 3", "1"},
	}

	for _, tt := range tests {
		ref := &EmbedRef{Type: "math", Params: map[string]string{"value": tt.expr}, IsEarly: true}
		got, err := r.Resolve(ctx, ref)
		if err != nil {
			t.Errorf("math(%q): unexpected error: %v", tt.expr, err)
			continue
		}
		if got != tt.want {
			t.Errorf("math(%q) = %q, want %q", tt.expr, got, tt.want)
		}
	}
}

func TestResolveMathFunctions(t *testing.T) {
	r := newTestResolver(nil)
	ctx := context.Background()

	tests := []struct {
		expr string
		want string
	}{
		{"sqrt(144)", "12.0"},
		{"abs(-5)", "5.0"},
		{"ceil(3.2)", "4.0"},
		{"floor(3.9)", "3.0"},
		{"round(3.5)", "4.0"},
		{"factorial(5)", "120"},
	}

	for _, tt := range tests {
		ref := &EmbedRef{Type: "math", Params: map[string]string{"value": tt.expr}, IsEarly: true}
		got, err := r.Resolve(ctx, ref)
		if err != nil {
			t.Errorf("math(%q): unexpected error: %v", tt.expr, err)
			continue
		}
		if got != tt.want {
			t.Errorf("math(%q) = %q, want %q", tt.expr, got, tt.want)
		}
	}
}

func TestResolveMathWithFormatSpec(t *testing.T) {
	r := newTestResolver(nil)
	ctx := context.Background()

	ref := &EmbedRef{Type: "math", Params: map[string]string{"value": "pi | .4f"}, IsEarly: true}
	got, err := r.Resolve(ctx, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "3.1416" {
		t.Errorf("math(pi | .4f) = %q, want %q", got, "3.1416")
	}
}

func TestResolveMathConstants(t *testing.T) {
	r := newTestResolver(nil)
	ctx := context.Background()

	ref := &EmbedRef{Type: "math", Params: map[string]string{"value": "pi"}, IsEarly: true}
	got, err := r.Resolve(ctx, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	piStr := strconv.FormatFloat(math.Pi, 'f', -1, 64)
	if got != piStr {
		t.Errorf("math(pi) = %q, want %q", got, piStr)
	}
}

func TestResolveDatetimeKeywords(t *testing.T) {
	r := newTestResolver(nil)
	ctx := context.Background()

	keywords := []string{"", "now", "iso"}
	for _, kw := range keywords {
		ref := &EmbedRef{Type: "datetime", Params: map[string]string{"value": kw}, IsEarly: true}
		got, err := r.Resolve(ctx, ref)
		if err != nil {
			t.Errorf("datetime(%q): unexpected error: %v", kw, err)
			continue
		}
		if !strings.Contains(got, "T") {
			t.Errorf("datetime(%q) = %q, doesn't look like RFC3339", kw, got)
		}
	}
}

func TestResolveDatetimeDate(t *testing.T) {
	r := newTestResolver(nil)
	ctx := context.Background()

	ref := &EmbedRef{Type: "datetime", Params: map[string]string{"value": "date"}, IsEarly: true}
	got, err := r.Resolve(ctx, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 10 || got[4] != '-' || got[7] != '-' {
		t.Errorf("datetime(date) = %q, want YYYY-MM-DD format", got)
	}
}

func TestResolveDatetimeTimestamp(t *testing.T) {
	r := newTestResolver(nil)
	ctx := context.Background()

	ref := &EmbedRef{Type: "datetime", Params: map[string]string{"value": "timestamp"}, IsEarly: true}
	got, err := r.Resolve(ctx, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := strconv.ParseInt(got, 10, 64); err != nil {
		t.Errorf("datetime(timestamp) = %q, not a valid integer", got)
	}
}

func TestResolveDatetimeStrftime(t *testing.T) {
	r := newTestResolver(nil)
	ctx := context.Background()

	ref := &EmbedRef{Type: "datetime", Params: map[string]string{"value": "%Y/%m/%d"}, IsEarly: true}
	got, err := r.Resolve(ctx, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 10 || got[4] != '/' || got[7] != '/' {
		t.Errorf("datetime(%%Y/%%m/%%d) = %q, want YYYY/MM/DD format", got)
	}
}

func TestResolveUUID(t *testing.T) {
	r := newTestResolver(nil)
	ctx := context.Background()

	ref := &EmbedRef{Type: "uuid", Params: map[string]string{}, IsEarly: true}
	got, err := r.Resolve(ctx, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 36 || got[8] != '-' || got[13] != '-' {
		t.Errorf("uuid = %q, doesn't look like a UUID", got)
	}

	got2, _ := r.Resolve(ctx, ref)
	if got == got2 {
		t.Error("two uuid calls returned the same value")
	}
}

func TestResolveStatusUpdate(t *testing.T) {
	r := newTestResolver(nil)
	ctx := context.Background()

	ref := &EmbedRef{Type: "status_update", Params: map[string]string{"value": "Searching database..."}, IsEarly: true}
	got, err := r.Resolve(ctx, ref)
	if err != ErrStatusUpdateSignal {
		t.Fatalf("expected ErrStatusUpdateSignal, got: %v", err)
	}
	if got != "Searching database..." {
		t.Errorf("status_update = %q, want %q", got, "Searching database...")
	}
}

func TestResolveStatusUpdateEmpty(t *testing.T) {
	r := newTestResolver(nil)
	ctx := context.Background()

	ref := &EmbedRef{Type: "status_update", Params: map[string]string{"value": ""}, IsEarly: true}
	got, err := r.Resolve(ctx, ref)
	if err != ErrStatusUpdateSignal {
		t.Fatalf("expected ErrStatusUpdateSignal, got: %v", err)
	}
	if got != "" {
		t.Errorf("status_update empty = %q, want empty", got)
	}
}

func TestSplitFormatSpec(t *testing.T) {
	tests := []struct {
		input    string
		wantExpr string
		wantFmt  string
	}{
		{"pi", "pi", ""},
		{"pi | .4f", "pi", ".4f"},
		{"2 + 2 | .2f", "2 + 2", ".2f"},
		{"some | thing | .3f", "some | thing", ".3f"},
		{`sqrt(2) \| .2f`, "sqrt(2)", ".2f"},
		{`(1 + sqrt(5)) / 2 \| .6f`, "(1 + sqrt(5)) / 2", ".6f"},
	}

	for _, tt := range tests {
		gotExpr, gotFmt := splitFormatSpec(tt.input)
		if gotExpr != tt.wantExpr || gotFmt != tt.wantFmt {
			t.Errorf("splitFormatSpec(%q) = (%q, %q), want (%q, %q)",
				tt.input, gotExpr, gotFmt, tt.wantExpr, tt.wantFmt)
		}
	}
}

func TestResolveEmbedsInStringMath(t *testing.T) {
	r := newTestResolver(nil)
	ctx := context.Background()

	input := "The answer is «math:2 + 2» and that's it."
	got := r.ResolveEmbedsInString(ctx, input)
	want := "The answer is 4 and that's it."
	if got != want {
		t.Errorf("ResolveEmbedsInString(%q) = %q, want %q", input, got, want)
	}
}

func TestResolveEmbedsInStringLateEmbed(t *testing.T) {
	r := newTestResolver(nil)
	ctx := context.Background()

	input := "See «artifact_content:data.csv» for details."
	got := r.ResolveEmbedsInString(ctx, input)
	if got != input {
		t.Errorf("ResolveEmbedsInString(%q) = %q, want unchanged", input, got)
	}
}

func TestResolveEmbedsInStringMultiple(t *testing.T) {
	r := newTestResolver(nil)
	ctx := context.Background()

	input := "Sum: «math:1 + 1», Date: «datetime:date»"
	got := r.ResolveEmbedsInString(ctx, input)
	if !strings.HasPrefix(got, "Sum: 2, Date: ") {
		t.Errorf("ResolveEmbedsInString multi = %q, unexpected prefix", got)
	}
}

func TestResolveEmbedsInStringNoEmbeds(t *testing.T) {
	r := newTestResolver(nil)
	ctx := context.Background()

	input := "No embeds here."
	got := r.ResolveEmbedsInString(ctx, input)
	if got != input {
		t.Errorf("ResolveEmbedsInString no embeds = %q, want unchanged", got)
	}
}

func TestResolveEmbedsInStringStatusUpdateStripped(t *testing.T) {
	r := newTestResolver(nil)
	ctx := context.Background()

	input := "Before «status_update:working on it» after"
	got := r.ResolveEmbedsInString(ctx, input)
	want := "Before  after"
	if got != want {
		t.Errorf("ResolveEmbedsInString status_update = %q, want %q", got, want)
	}
}

func TestResolveToolArgs(t *testing.T) {
	r := newTestResolver(nil)
	ctx := context.Background()

	args := map[string]any{
		"query":   "total is «math:10 * 5»",
		"raw_arg": "«math:1+1» should not resolve",
		"nested": map[string]any{
			"inner": "value is «math:3+3»",
		},
		"list": []any{"«math:7+7»", "plain"},
	}

	got := r.ResolveToolArgs(ctx, args, []string{"raw_arg"})

	if got["query"] != "total is 50" {
		t.Errorf("query = %q, want %q", got["query"], "total is 50")
	}
	if got["raw_arg"] != "«math:1+1» should not resolve" {
		t.Errorf("raw_arg was resolved when it should have been exempt")
	}
	nested := got["nested"].(map[string]any)
	if nested["inner"] != "value is 6" {
		t.Errorf("nested.inner = %q, want %q", nested["inner"], "value is 6")
	}
	list := got["list"].([]any)
	if list[0] != "14" {
		t.Errorf("list[0] = %q, want %q", list[0], "14")
	}
}

func TestResolveMathDefersTemplateSyntax(t *testing.T) {
	r := newTestResolver(nil)
	ctx := context.Background()

	tests := []struct {
		name string
		expr string
	}{
		{"variable interpolation", "{{item.price}} * {{item.qty}}"},
		{"with format spec", "{{item.price}} * {{item.qty}} | .2f"},
		{"liquid tag", "{% if condition %}42{% endif %} + 1"},
		{"mixed", "{{a}} + {% if x %}{{b}}{% endif %}"},
	}

	for _, tt := range tests {
		raw := "«math:" + tt.expr + "»"
		ref := &EmbedRef{
			Type:    "math",
			Params:  map[string]string{"value": tt.expr},
			Raw:     raw,
			IsEarly: true,
		}
		got, err := r.Resolve(ctx, ref)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tt.name, err)
			continue
		}
		if got != raw {
			t.Errorf("%s: expected raw embed %q preserved, got %q", tt.name, raw, got)
		}
	}
}

func TestResolveMathExpressionExported(t *testing.T) {
	tests := []struct {
		value string
		want  string
	}{
		{"2 + 3", "5"},
		{"42.5 * 3", "127.5"},
		{"10 * 5 | .2f", "50.00"},
		{`sqrt(2) \| .2f`, "1.41"},
		{`pi \| .4f`, "3.1416"},
		{`22 / 7 \| .6f`, "3.142857"},
		{`(1 + sqrt(5)) / 2 \| .6f`, "1.618034"},
		{`e ** pi \| .4f`, "23.1407"},
		{"", "None"},
	}
	for _, tt := range tests {
		got, err := ResolveMathExpression(tt.value)
		if err != nil {
			t.Errorf("ResolveMathExpression(%q): unexpected error: %v", tt.value, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ResolveMathExpression(%q) = %q, want %q", tt.value, got, tt.want)
		}
	}
}

func TestResolveEmbedsInStringDefersTemplateSyntax(t *testing.T) {
	r := newTestResolver(nil)
	ctx := context.Background()

	input := "Total: «math:{{item.price}} * {{item.qty}}»"
	got := r.ResolveEmbedsInString(ctx, input)
	if got != input {
		t.Errorf("expected template-containing math embed preserved, got %q", got)
	}
}

func TestConvertStrftimeToGo(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"%Y-%m-%d", "2006-01-02"},
		{"%H:%M:%S", "15:04:05"},
		{"%Y/%m/%d %H:%M", "2006/01/02 15:04"},
	}

	for _, tt := range tests {
		got := convertStrftimeToGo(tt.input)
		if got != tt.want {
			t.Errorf("convertStrftimeToGo(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
