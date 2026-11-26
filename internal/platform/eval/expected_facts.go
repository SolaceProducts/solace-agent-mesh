// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"golang.org/x/text/unicode/norm"
)

type ExpectedFacts struct {
	Required  []string `json:"required,omitempty"`
	Forbidden []string `json:"forbidden,omitempty"`
}

const (
	maxExpectedFacts = 100
	maxFactChars     = 500
)

const expectedFactsColumn = "expected_facts"

const heuristicOutputFacts = "output_facts"

const reasonNoExpectedFacts = "No expected facts provided for this example"

func validateExpectedFacts(facts *ExpectedFacts) error {
	total := len(facts.Required) + len(facts.Forbidden)
	if total > maxExpectedFacts {
		return &ValidationError{Field: expectedFactsColumn, Message: "expectedFacts is invalid", Details: map[string][]string{
			expectedFactsColumn: {fmt.Sprintf("expectedFacts holds at most %d values", maxExpectedFacts)},
		}}
	}
	details := map[string][]string{}
	if total == 0 {
		details[expectedFactsColumn] = append(details[expectedFactsColumn], "expectedFacts needs at least one required or forbidden value")
	}
	seen := map[string]string{}
	checkFacts(details, "required", facts.Required, seen)
	checkFacts(details, "forbidden", facts.Forbidden, seen)
	if len(details) == 0 {
		return nil
	}
	return &ValidationError{Field: expectedFactsColumn, Message: "expectedFacts is invalid", Details: details}
}

func checkFacts(details map[string][]string, list string, values []string, seen map[string]string) {
	field := expectedFactsColumn + "." + list
	for _, v := range values {
		normalized := normalizeFact(v)
		switch {
		case normalized == "":
			details[field] = append(details[field], "a value must not be blank")
		case utf8.RuneCountInString(v) > maxFactChars:
			details[field] = append(details[field], fmt.Sprintf("%.40q…: a value holds at most %d characters", v, maxFactChars))
		case seen[normalized] == list:
			details[field] = append(details[field], fmt.Sprintf("%q is listed more than once", v))
		case seen[normalized] != "":
			details[field] = append(details[field], fmt.Sprintf("%q is both required and forbidden", v))
		default:
			seen[normalized] = list
		}
	}
}

var factsObject = exampleObject[ExpectedFacts]{
	column: expectedFactsColumn, key: "expectedFacts", shape: "required and forbidden lists", validate: validateExpectedFacts,
}

func encodeExpectedFacts(facts *ExpectedFacts) (*string, error) { return factsObject.encode(facts) }

func decodeExpectedFacts(raw *string) (*ExpectedFacts, error) { return factsObject.decode(raw) }

func shownExpectedFacts(raw *string) *ExpectedFacts { return factsObject.shown(raw) }

func ParseExpectedFactsCell(cell string) (*ExpectedFacts, error) { return factsObject.checkCell(cell) }

func encodeExpectedFactsCell(cell string) (*string, error) { return factsObject.encodeCell(cell) }

func SameExpectedFacts(a, b *ExpectedFacts) bool { return factsObject.same(a, b) }

func importedExpectedFacts(raw json.RawMessage) (*string, error) {
	return factsObject.encodeImported(raw)
}

func sameExpectedFacts(stored *string, desired *ExpectedFacts) bool {
	return factsObject.sameStored(stored, desired)
}

type responseFacts struct {
	text    string
	numbers []*big.Rat
}

func readResponseFacts(response string) responseFacts {
	payload, ok := jsonPayload(response)
	if !ok {
		return responseFacts{text: normalizeFact(response)}
	}
	leaves := leafCollector{values: map[string]bool{}}
	leaves.add(payload)
	return responseFacts{text: strings.Join(slices.Sorted(maps.Keys(leaves.values)), " \u0000 "), numbers: leaves.numbers}
}

func jsonPayload(response string) (any, bool) {
	dec := json.NewDecoder(strings.NewReader(unfenced(strings.TrimSpace(response))))
	dec.UseNumber()
	var payload any
	if dec.Decode(&payload) != nil {
		return nil, false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, false
	}
	return payload, true
}

func unfenced(s string) string {
	const fence = "```"
	if len(s) < 2*len(fence) || !strings.HasPrefix(s, fence) || !strings.HasSuffix(s, fence) {
		return s
	}
	_, body, _ := strings.Cut(s[len(fence):len(s)-len(fence)], "\n")
	return body
}

type leafCollector struct {
	values  map[string]bool
	numbers []*big.Rat
}

func (c *leafCollector) add(v any) {
	switch t := v.(type) {
	case map[string]any:
		for _, child := range t {
			c.add(child)
		}
	case []any:
		for _, child := range t {
			c.add(child)
		}
	case string:
		c.values[normalizeFact(t)] = true
	case json.Number:
		literal := strings.ToLower(t.String())
		c.values[literal] = true
		if n, ok := exactNumber(literal); ok {
			c.numbers = append(c.numbers, n)
		}
	case bool:
		c.values[strconv.FormatBool(t)] = true
	}
}

var jsonNumberPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?(e[+-]?([0-9]+))?$`)

const (
	maxNumberExponent = 400
	maxNumberLength   = 100
)

func exactNumber(s string) (*big.Rat, bool) {
	if len(s) > maxNumberLength {
		return nil, false
	}
	m := jsonNumberPattern.FindStringSubmatch(s)
	if m == nil {
		return nil, false
	}
	if exp, err := strconv.Atoi(m[4]); m[4] != "" && (err != nil || exp > maxNumberExponent) {
		return nil, false
	}
	return new(big.Rat).SetString(s)
}

func normalizeFact(s string) string {
	return strings.Join(strings.Fields(norm.NFC.String(strings.ToLower(norm.NFC.String(s)))), " ")
}

func (r responseFacts) says(fact string) bool {
	want := normalizeFact(fact)
	if want == "" {
		return false
	}
	if n, ok := exactNumber(want); ok && slices.ContainsFunc(r.numbers, func(v *big.Rat) bool { return v.Cmp(n) == 0 }) {
		return true
	}
	first, last := newFactEdge(want, false), newFactEdge(want, true)
	for from := 0; ; {
		i := strings.Index(r.text[from:], want)
		if i < 0 {
			return false
		}
		start := from + i
		if !first.continuesBefore(r.text[:start]) && !last.continuesAfter(r.text[start+len(want):]) {
			return true
		}
		from = start + 1
	}
}

type factEdge struct {
	r       rune
	checked bool
	script  tokenScript
}

func newFactEdge(fact string, last bool) factEdge {
	r, _ := utf8.DecodeRuneInString(fact)
	if last {
		r, _ = utf8.DecodeLastRuneInString(fact)
	}
	return factEdge{r: r, checked: isSpacedWordRune(r), script: scriptOf(r)}
}

func (e factEdge) continuesBefore(before string) bool {
	if !e.checked {
		return false
	}
	r, size := utf8.DecodeLastRuneInString(before)
	if e.continuesToken(r) || (r == '-' && unicode.IsDigit(e.r)) {
		return true
	}
	beyond, _ := utf8.DecodeLastRuneInString(before[:len(before)-size])
	return joins(r, beyond, e.r)
}

func (e factEdge) continuesAfter(after string) bool {
	if !e.checked {
		return false
	}
	r, size := utf8.DecodeRuneInString(after)
	if e.continuesToken(r) {
		return true
	}
	beyond, _ := utf8.DecodeRuneInString(after[size:])
	return joins(r, beyond, e.r)
}

func joins(r, beyond, edge rune) bool {
	switch r {
	case '-', '_', '.':
		return isWordRune(beyond)
	case ',':
		return unicode.IsDigit(edge) && unicode.IsDigit(beyond)
	}
	return false
}

func isWordRune(r rune) bool {
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return true
	}
	return unicode.IsMark(r) && !unicode.Is(unicode.Variation_Selector, r) && !unicode.Is(unicode.Me, r)
}

var unspacedScripts = []*unicode.RangeTable{
	unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Thai, unicode.Lao, unicode.Khmer, unicode.Myanmar,
}

func isSpacedWordRune(r rune) bool {
	return isWordRune(r) && !unicode.In(r, unspacedScripts...)
}

func (e factEdge) continuesToken(neighbour rune) bool {
	if !isSpacedWordRune(neighbour) {
		return false
	}
	if unicode.IsMark(neighbour) || unicode.IsMark(e.r) {
		return true
	}
	return e.script.has(neighbour)
}

type tokenScript struct {
	latin bool
	table *unicode.RangeTable
}

func scriptOf(r rune) tokenScript {
	if isLatinToken(r) {
		return tokenScript{latin: true}
	}
	for _, table := range unicode.Scripts {
		if unicode.Is(table, r) {
			return tokenScript{table: table}
		}
	}
	return tokenScript{}
}

func (s tokenScript) has(r rune) bool {
	if isLatinToken(r) {
		return s.latin
	}
	return s.table != nil && unicode.Is(s.table, r)
}

func isLatinToken(r rune) bool {
	return unicode.IsDigit(r) || unicode.Is(unicode.Latin, r)
}

func outputFactsScore(in ScoreInputs) ScoreOutput {
	if in.ExpectedFactsUnreadable {
		return erroredScore("this example's stored expected facts cannot be read; set them again")
	}
	if in.ExpectedFacts == nil || len(in.ExpectedFacts.Required)+len(in.ExpectedFacts.Forbidden) == 0 {
		reason := reasonNoExpectedFacts
		return ScoreOutput{Reasoning: &reason}
	}
	said := readResponseFacts(in.Response)
	var missing, present []string
	for _, fact := range in.ExpectedFacts.Required {
		if !said.says(fact) {
			missing = append(missing, strconv.Quote(fact))
		}
	}
	for _, fact := range in.ExpectedFacts.Forbidden {
		if said.says(fact) {
			present = append(present, strconv.Quote(fact))
		}
	}
	total := len(in.ExpectedFacts.Required) + len(in.ExpectedFacts.Forbidden)
	kept := total - len(missing) - len(present)
	reason := fmt.Sprintf("%d of %d facts held.", kept, total)
	if len(missing) > 0 {
		reason += " Missing: " + strings.Join(missing, ", ") + "."
	}
	if len(present) > 0 {
		reason += " Present but forbidden: " + strings.Join(present, ", ") + "."
	}
	return ScoreOutput{Score: float64(kept) / float64(total), Passed: kept == total, Reasoning: &reason}
}

func isOutputFactsEvaluator(ev *store.EvalEvaluator) bool {
	return ev.Type == evaluatorTypeHeuristic && ev.HeuristicType != nil && *ev.HeuristicType == heuristicOutputFacts
}
