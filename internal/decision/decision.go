// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package decision

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	ProviderTypeSafe       = "typesafe"
	ProviderOpenAI         = "openai"
	ProviderGoogleAIStudio = "google_ai_studio"
)

type Evaluator interface {
	Evaluate(ctx context.Context, req Request) (Result, error)
}

type QuestionType string

const (
	TypeBoolean QuestionType = "boolean"
	TypeChoice  QuestionType = "choice"
	TypeScore   QuestionType = "score"
)

const MaxCriteria = 255

const MaxScoreLevels = 10

const (
	BooleanCriterionTrue  = "true"
	BooleanCriterionFalse = "false"
)

type Criterion struct {
	Key         string
	Description string
}

type Question struct {
	Key          string
	Type         QuestionType
	Instructions string
	Criteria     []Criterion
}

func (q Question) CriterionKeys() []string {
	keys := make([]string, len(q.Criteria))
	for i, c := range q.Criteria {
		keys[i] = c.Key
	}
	return keys
}

func (q Question) HasCriterion(key string) bool {
	return slices.Contains(q.CriterionKeys(), key)
}

func (q Question) Validate() error {
	if strings.TrimSpace(q.Instructions) == "" {
		return errors.New("'instructions' is required")
	}
	seen := make(map[string]bool, len(q.Criteria))
	for i, c := range q.Criteria {
		if strings.TrimSpace(c.Key) == "" {
			return fmt.Errorf("criteria[%d] needs an option key", i)
		}
		if seen[strings.TrimSpace(c.Key)] {
			return fmt.Errorf("criteria[%d] repeats option %q", i, c.Key)
		}
		seen[strings.TrimSpace(c.Key)] = true
	}
	switch q.Type {
	case TypeBoolean:
		if err := validateBooleanCriteria(q.Criteria); err != nil {
			return err
		}
	case TypeChoice, TypeScore:
		if len(q.Criteria) < 2 {
			return fmt.Errorf("a %s question needs at least two 'criteria'", q.Type)
		}
		if q.Type == TypeScore && len(q.Criteria) > MaxScoreLevels {
			return fmt.Errorf("a score question takes at most %d levels, got %d", MaxScoreLevels, len(q.Criteria))
		}
	default:
		return fmt.Errorf("'type' must be one of boolean, choice, score; got %q", q.Type)
	}
	if len(q.Criteria) > MaxCriteria {
		return fmt.Errorf("%d criteria exceeds the limit of %d", len(q.Criteria), MaxCriteria)
	}
	return nil
}

func validateBooleanCriteria(criteria []Criterion) error {
	described := 0
	for _, c := range criteria {
		if c.Key != BooleanCriterionTrue && c.Key != BooleanCriterionFalse {
			return fmt.Errorf("a boolean question's 'criteria' may only describe %q and %q, got %q",
				BooleanCriterionTrue, BooleanCriterionFalse, c.Key)
		}
		if c.Description != "" {
			described++
		}
	}
	if described == 1 {
		return fmt.Errorf("a boolean question's 'criteria' must describe both %q and %q, or be left out",
			BooleanCriterionTrue, BooleanCriterionFalse)
	}
	return nil
}

type Request struct {
	Model           string
	State           any
	Media           []Media
	Questions       []Question
	Logger          *slog.Logger
	ImageResolution string
	VideoResolution string
	ImageBytesLimit int64
}

type MediaKind string

const (
	MediaImage    MediaKind = "image"
	MediaAudio    MediaKind = "audio"
	MediaVideo    MediaKind = "video"
	MediaDocument MediaKind = "document"
)

type Media struct {
	Name     string
	Kind     MediaKind
	MIMEType string
	Data     []byte
}

func ImagesOf(req Request) []Media {
	return OfKind(req.Media, MediaImage)
}

func OfKind(media []Media, kind MediaKind) []Media {
	var out []Media
	for _, m := range media {
		if m.Kind == kind {
			out = append(out, m)
		}
	}
	return out
}

type Answer struct {
	Type          QuestionType
	Probability   float64
	Choice        string
	Score         float64
	Legend        map[string]any
	Probabilities map[string]float64
	Confidence    *float64
}

type Usage struct {
	InputTokens  int
	OutputTokens int
}

type Result struct {
	Model         string
	Provider      string
	Answers       map[string]Answer
	Usage         Usage
	ModelConfigID string
}

func (a Answer) Output() map[string]any {
	out := map[string]any{"type": string(a.Type)}
	switch a.Type {
	case TypeBoolean:
		out["value"] = a.Probability >= 0.5
		out["probability"] = a.Probability
	case TypeChoice:
		out["choice"] = a.Choice
		out["probabilities"] = probabilitiesOutput(a.Probabilities)
		out["confidence"] = confidenceOutput(a.Confidence)
	case TypeScore:
		out["score"] = a.Score
		out["legend"] = a.Legend
		out["probabilities"] = probabilitiesOutput(a.Probabilities)
		out["confidence"] = confidenceOutput(a.Confidence)
	}
	return out
}

func probabilitiesOutput(p map[string]float64) map[string]any {
	out := make(map[string]any, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}

func confidenceOutput(c *float64) any {
	if c == nil {
		return nil
	}
	return *c
}

type FailureKind string

const (
	FailureUnauthorized    FailureKind = "unauthorized"
	FailureRateLimited     FailureKind = "rate_limited"
	FailureTimeout         FailureKind = "timeout"
	FailureUnreachable     FailureKind = "unreachable"
	FailureInvalidResponse FailureKind = "invalid_response"
	FailureInputTooLarge   FailureKind = "input_too_large"
	FailureConfig          FailureKind = "config"
	FailureRefused         FailureKind = "refused"
)

func (k FailureKind) Permanent() bool {
	return k == FailureUnauthorized || k == FailureConfig
}

func (k FailureKind) Systemic() bool {
	switch k {
	case FailureUnauthorized, FailureConfig, FailureUnreachable, FailureRateLimited, FailureTimeout:
		return true
	}
	return false
}

type Error struct {
	Kind   FailureKind
	Status int
	Err    error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

func KeyRejected(err error) bool {
	if kr, ok := errors.AsType[interface {
		error
		KeyRejected() bool
	}](err); ok {
		return kr.KeyRejected()
	}
	status, ok := HTTPStatus(err)
	return ok && status == http.StatusUnauthorized && Classify(err) == FailureUnauthorized
}

func HTTPStatus(err error) (int, bool) {
	se, ok := errors.AsType[interface {
		error
		HTTPStatus() int
	}](err)
	if !ok {
		return 0, false
	}
	return se.HTTPStatus(), true
}

func Classify(err error) FailureKind {
	if de, ok := errors.AsType[*Error](err); ok {
		return de.Kind
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return FailureTimeout
	}
	return FailureUnreachable
}

const (
	maxProviderStringBytes = 64
	maxListedAnswerKeys    = 5
)

func BoundedQuote(s string) string {
	bounded := BoundedString(s)
	if len(bounded) == len(s) {
		return strconv.Quote(s)
	}
	return fmt.Sprintf("%s... (%d bytes)", strconv.Quote(bounded), len(s))
}

func BoundedString(s string) string {
	if len(s) <= maxProviderStringBytes {
		return s
	}
	cut := maxProviderStringBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func boundedKeyList(keys []string) string {
	shown := keys[:min(len(keys), maxListedAnswerKeys)]
	quoted := make([]string, len(shown))
	for i, k := range shown {
		quoted[i] = BoundedQuote(k)
	}
	list := "[" + strings.Join(quoted, " ") + "]"
	if hidden := len(keys) - len(shown); hidden > 0 {
		list += fmt.Sprintf(" and %d more", hidden)
	}
	return list
}

func invalidResponse(format string, args ...any) error {
	return &Error{Kind: FailureInvalidResponse, Err: fmt.Errorf(format, args...)}
}

func isProbability(v float64) bool {
	return v >= 0 && v <= 1
}

func (r Result) Validate(questions []Question) error {
	for _, q := range questions {
		a, ok := r.Answers[q.Key]
		if !ok {
			return invalidResponse("decision provider returned no answer for question %q (answered: %s)",
				q.Key, boundedKeyList(slices.Sorted(maps.Keys(r.Answers))))
		}
		if a.Type != q.Type {
			return invalidResponse("decision provider answered question %q as %s, but it asked for %s",
				q.Key, BoundedQuote(string(a.Type)), q.Type)
		}
		if err := a.validateValues(q); err != nil {
			return err
		}
	}
	return nil
}

func (a Answer) validateValues(q Question) error {
	switch a.Type {
	case TypeBoolean:
		if !isProbability(a.Probability) {
			return invalidResponse("decision provider returned probability %v for question %q, outside [0,1]", a.Probability, q.Key)
		}
		return nil
	case TypeChoice:
		if !q.HasCriterion(a.Choice) {
			return invalidResponse("decision provider chose %s for question %q, which is not one of its criteria %v",
				BoundedQuote(a.Choice), q.Key, q.CriterionKeys())
		}
	case TypeScore:
		if highest := float64(len(q.Criteria) - 1); highest > 0 && (a.Score < 0 || a.Score > highest) {
			return invalidResponse("decision provider returned score %v for question %q, outside its levels [0,%v]", a.Score, q.Key, highest)
		}
	}
	if a.Confidence != nil && !isProbability(*a.Confidence) {
		return invalidResponse("decision provider returned confidence %v for question %q, outside [0,1]", *a.Confidence, q.Key)
	}
	for _, k := range slices.Sorted(maps.Keys(a.Probabilities)) {
		if v := a.Probabilities[k]; !isProbability(v) {
			return invalidResponse("decision provider returned probability %v for option %s of question %q, outside [0,1]",
				v, BoundedQuote(k), q.Key)
		}
	}
	return nil
}
