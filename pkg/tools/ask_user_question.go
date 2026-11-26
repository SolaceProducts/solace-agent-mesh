// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/hilsurface"
)

const (
	hilDefaultTimeout = 45 * time.Minute
	hilSourceAsk      = "ask_user_question"
)

const noRealChoicesEscape = " If you do not actually have concrete choices to offer, drop " +
	"`options` and ask in prose instead."

const samRequiredPlaceholder = "<<__SAM_REQUIRED__>>"

const secretFieldRefusal = "ask_user_question was not used: %q on component %q is a value " +
	"the platform stores as a secret, and the component's config artifact still carries " +
	samRequiredPlaceholder + " there. This form has no masked field: the value would be " +
	"drawn in the clear as it is typed and carried back into the conversation. Nothing was " +
	"shown to the user. Leave the " + samRequiredPlaceholder + " placeholder in place and " +
	"name the outstanding fields in your reply instead. The non-secret values the component " +
	"still needs — a hostname, a port, a region — can be collected with this form as usual."

type AskUserQuestionTool struct {
	artifactToolBase
	tool.SideEffectingTool
}

func (t *AskUserQuestionTool) Name() string { return "ask_user_question" }

func (t *AskUserQuestionTool) Description() string {
	return "Render a structured follow-up form in the user's chat. The DEFAULT way to " +
		"ask the user something is to reply in plain text in the conversation — do NOT " +
		"call this tool for ordinary clarifying questions, simple confirmations, or " +
		"anything that fits in a one- or two-sentence reply. Only use this tool when " +
		"the structured form genuinely helps the user: (a) bounded multiple-choice " +
		"between 2-4 concrete, mutually exclusive options where free-text guesses " +
		"would be ambiguous; (b) collecting multi-field structured input in one shot " +
		"(e.g. a component's required configuration values) — give every " +
		"field a placeholder, which is what permits their free-text fields; or (c) an " +
		"explicit confirmation before an irreversible or high-stakes action. Do NOT " +
		"use it for: open-ended questions (\"what should I name this?\", \"any other " +
		"requirements?\"), anything you could ask conversationally, or just because " +
		"you feel uncertain — uncertainty alone is not a reason to interrupt the chat " +
		"with a form. When the only thing you need is a free-text answer, write the " +
		"question as a normal assistant message instead. A call in which every question " +
		"is free-text (zero options) is refused at runtime unless every one of those " +
		"questions carries a placeholder naming the value you want typed in. Never pad an " +
		"options list, and never invent a placeholder for a question that does not collect " +
		"a specific value, to get past that — ask in prose instead. Do NOT use this form to " +
		"collect passwords, API keys, tokens or other secrets: the field is not masked, and " +
		"the answer is carried back into the conversation. A question aimed at a secret field " +
		"the component's own config still carries as " + samRequiredPlaceholder + " is refused " +
		"at runtime — leave that placeholder alone and name the outstanding fields in your " +
		"reply. Non-secret configuration a component is still missing is fair game for this " +
		"form even while it carries the same placeholder."
}

func (t *AskUserQuestionTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"message": map[string]any{
				"type": "string",
				"description": "Overall context explaining why you are asking. " +
					"Displayed above the questions.",
			},
			"component_name": map[string]any{
				"type": "string",
				"description": "Optional. The name of the component this question relates to " +
					"(e.g. a connector name when asking for its configuration). " +
					"Passed through to the UI for status tracking. Set it whenever you are " +
					"collecting a component's configuration values.",
			},
			"questions": map[string]any{
				"type":        "array",
				"minItems":    1,
				"maxItems":    4,
				"description": "The questions to ask the user.",
				"items": map[string]any{
					"type":     "object",
					"required": []string{"question", "header"},
					"properties": map[string]any{
						"question": map[string]any{
							"type": "string",
							"description": "The complete question to ask. Should be clear, " +
								"specific, and end with a question mark.",
						},
						"header": map[string]any{
							"type":      "string",
							"maxLength": 12,
							"description": "Very short label for this question " +
								"(e.g., \"Volume\", \"Access\", \"Confirm\"). Shown as the tab " +
								"label when the call carries several questions, and as the choice " +
								"group's label on a single multiple-choice card; a single " +
								"free-text card does not show it.",
						},
						"options": map[string]any{
							"type":     "array",
							"minItems": 0,
							"maxItems": 4,
							"description": "The available choices. When provided (1-4 items), " +
								"renders as radio buttons / checkboxes with an \"Other\" " +
								"free-text option appended automatically. Each item must be an " +
								"object with a non-empty `label` and a `description`; bare strings, " +
								"objects without a usable `label`, and a scalar supplied in place of " +
								"a list are refused. When " +
								"empty or omitted this question renders as a text input. What permits " +
								"one is the value it collects, named in its `placeholder`; standing it " +
								"next to a multiple-choice question is a layout choice, not a way past " +
								"a rule. A text input aimed at a secret the component's config still " +
								"leaves unfilled is refused whichever way the call is shaped.",
							"items": map[string]any{
								"type":     "object",
								"required": []string{"label", "description"},
								"properties": map[string]any{
									"label": map[string]any{
										"type":        "string",
										"description": "Display text for the option (1-5 words).",
									},
									"description": map[string]any{
										"type":        "string",
										"description": "Explanation of what this option means.",
									},
								},
							},
						},
						"placeholder": map[string]any{
							"type": "string",
							"description": "Placeholder text for the text input field. " +
								"Only used when options is empty or omitted (text-only questions). " +
								"Example: \"e.g., PostgreSQL on AWS RDS\". Set it on every question " +
								"when the whole call collects specific values from the user's own " +
								"system — an all-free-text call is accepted only then, and the " +
								"placeholder is what names the value, so do not attach one to a " +
								"question that is not collecting a specific value.",
						},
						"multiSelect": map[string]any{
							"type":    "boolean",
							"default": false,
							"description": "If true, the user can select multiple options. " +
								"Only relevant when options are provided.",
						},
					},
				},
			},
		},
		Required: []string{"questions"},
	}
}

func (t *AskUserQuestionTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "status", typ: "string",
			description: "The user's outcome: \"answered\", \"cancelled\", \"timed_out\", or an unrecognised status passed through from the HIL layer. Never \"success\"."},
		resultField{name: "answers", typ: "object", optional: true,
			description: "Present only when status is \"answered\". Open: each property name is the question's own text and each value is the answer as a string, multi-select joined by commas."},
		resultField{name: "message", typ: "string", optional: true,
			description: "Explains a non-answered outcome. Present on every status except \"answered\"."},
		resultField{name: "error_code", typ: "string", optional: true,
			description: "Present only when status is \"timed_out\": the machine code of a question nobody answered."},
	)
}

func (t *AskUserQuestionTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	if !toolCtx.CanPromptUser() {
		return tool.Error("This channel can't show interactive prompts. "+
			"Ask the user directly in your next message, or use the web UI for guided input.",
			tool.WithErrorCode(tool.DispatchErrorCode(taxonomy.DispatchClassUserInputUnavailable))), nil
	}

	questions := coerceToSlice(args["questions"])
	if len(questions) == 0 {
		return tool.Error("At least one question is required."), nil
	}

	qs := questionMaps(questions)
	if len(qs) == 0 {
		return tool.Error(fmt.Sprintf("ask_user_question was not used: %d entries were supplied "+
			"but none is a question object. Each entry needs at least `question` and `header`.",
			len(questions))), nil
	}

	if text, bad := offendingQuestionText(qs); bad {
		if text == "" {
			return tool.Error("ask_user_question was not used: every question needs a non-empty " +
				"question string — it is the form's only label, and the key your answer comes " +
				"back under."), nil
		}
		return tool.Error(fmt.Sprintf("ask_user_question was not used: two questions share the "+
			"text %q. Answers are returned keyed by question text, so one would silently "+
			"overwrite the other. Give each question a distinct string.", text)), nil
	}

	switch q, problem := unusableOptions(qs); problem {
	case optionsNotObjects:
		return tool.Error(fmt.Sprintf("ask_user_question was not used: the question %q supplied "+
			"options, but none of them are objects, so it would have rendered as a plain text "+
			"box. Each option must be an object with `label` and `description` — an array of "+
			"bare strings is not accepted. Fix that question and resend; the other questions "+
			"in the call were fine.%s", q, noRealChoicesEscape)), nil
	case optionsMissingLabels:
		return tool.Error(fmt.Sprintf("ask_user_question was not used: the question %q supplied "+
			"option objects, but none of them carries a usable `label`, so nothing would have "+
			"rendered. Every option needs a non-empty `label` string naming the choice, plus a "+
			"`description`. Fix that question and resend; the other questions in the call were "+
			"fine.%s", q, noRealChoicesEscape)), nil
	}

	if allQuestionsAreTextOnly(qs) && !everyQuestionHasPlaceholder(qs) {
		return tool.Error(
			"ask_user_question was not used: every question in this call is free-text " +
				"(zero options) and at least one carries no placeholder, which makes it " +
				"identical to asking in prose. Nothing was shown to the user. Ask these " +
				"questions directly in your next assistant message instead. The one " +
				"exception is collecting specific values from the user's own system " +
				"(a component's configuration values): there, every question names " +
				"its value in a placeholder. Do NOT invent choices, and do NOT attach a " +
				"placeholder to a question that is not collecting such a value, to get " +
				"past this — a form of made-up choices, or a text box for a question the " +
				"user could simply have been asked, is worse than the plain question it " +
				"replaced."), nil
	}

	componentName, _ := args["component_name"].(string)
	if field, component, targeted := placeholderedSecretFieldTargeted(ctx, textOnlyQuestions(qs), componentName, toolCtx); targeted {
		return tool.Error(fmt.Sprintf(secretFieldRefusal, field, component)), nil
	}

	requestID := idgen.New()

	surface, err := generateA2UISurface(requestID, args)
	if err != nil {
		return nil, fmt.Errorf("generate A2UI surface: %w", err)
	}

	timeout := hilDefaultTimeout
	if toolCtx.ToolConfig != nil {
		if v, ok := toolCtx.ToolConfig["hil_default_timeout"]; ok {
			if s, ok := v.(string); ok {
				if d, err := time.ParseDuration(s); err == nil {
					timeout = d
				}
			}
		}
	}

	if toolCtx.PauseResume && toolCtx.PublishUserInput != nil {
		toolCtx.PublishUserInput(ctx, requestID, hilSourceAsk, surface, timeout)
		return tool.Pending("Waiting for the user's answer", tool.WithData(map[string]any{
			"pending_kind":   hilsurface.PendingKindAsk,
			"hil_request_id": requestID,
			"hil_timeout_ns": float64(timeout),
			"questions":      questions,
		}), tool.WithTrustedPending(tool.TrustedPendingHILAsk)), nil
	}

	resp := toolCtx.WaitForUserInput(ctx, requestID, hilSourceAsk, surface, timeout) //nolint:staticcheck // WaitForUserInput still used in non-pause/resume mode
	return hilsurface.AskUserQuestionResult(questions, resp, timeout), nil
}

func rendersAsTextOnly(qm map[string]any) bool {
	return len(renderableOptions(qm)) == 0
}

func allQuestionsAreTextOnly(qs []map[string]any) bool {
	for _, qm := range qs {
		if !rendersAsTextOnly(qm) {
			return false
		}
	}
	return true
}

func textOnlyQuestions(qs []map[string]any) []map[string]any {
	textOnly := make([]map[string]any, 0, len(qs))
	for _, qm := range qs {
		if rendersAsTextOnly(qm) {
			textOnly = append(textOnly, qm)
		}
	}
	return textOnly
}

func offendingQuestionText(qs []map[string]any) (string, bool) {
	seen := make(map[string]bool, len(qs))
	for _, qm := range qs {
		raw, _ := qm["question"].(string)
		text := strings.TrimSpace(raw)
		if text == "" {
			return "", true
		}
		if seen[text] {
			return text, true
		}
		seen[text] = true
	}
	return "", false
}

type optionsProblem int

const (
	optionsRenderable optionsProblem = iota
	optionsNotObjects
	optionsMissingLabels
)

func unusableOptions(qs []map[string]any) (string, optionsProblem) {
	for _, qm := range qs {
		raw, present := qm["options"]
		if !present {
			continue
		}
		supplied := coerceToSlice(raw)
		text, _ := qm["question"].(string)
		if len(supplied) == 0 {
			if denotesNoOptions(raw) {
				continue
			}
			return text, optionsNotObjects
		}
		if len(renderableOptions(qm)) > 0 {
			continue
		}
		for _, o := range supplied {
			if _, isObject := o.(map[string]any); isObject {
				return text, optionsMissingLabels
			}
		}
		return text, optionsNotObjects
	}
	return "", optionsRenderable
}

func denotesNoOptions(v any) bool {
	switch val := v.(type) {
	case nil:
		return true
	case []any:
		return len(val) == 0
	case string:
		if strings.TrimSpace(val) == "" {
			return true
		}
		var decoded any
		if json.Unmarshal([]byte(val), &decoded) != nil {
			return false
		}
		switch d := decoded.(type) {
		case nil:
			return true
		case []any:
			return len(d) == 0
		}
	}
	return false
}

func everyQuestionHasPlaceholder(qs []map[string]any) bool {
	for _, qm := range qs {
		if s, _ := qm["placeholder"].(string); strings.TrimSpace(s) == "" {
			return false
		}
	}
	return true
}

func placeholderedSecretFieldTargeted(ctx context.Context, qs []map[string]any, componentName string, toolCtx *tool.Context) (string, string, bool) {
	if toolCtx == nil || toolCtx.ArtifactService == nil {
		return "", "", false
	}
	if named := strings.TrimSpace(componentName); named != "" {
		field, targeted := secretFieldTargetedOn(ctx, qs, named, toolCtx)
		return field, named, targeted
	}
	for _, name := range manifestComponentNames(ctx, toolCtx) {
		if field, targeted := secretFieldTargetedOn(ctx, qs, name, toolCtx); targeted {
			return field, name, true
		}
	}
	return "", "", false
}

func secretFieldTargetedOn(ctx context.Context, qs []map[string]any, componentName string, toolCtx *tool.Context) (string, bool) {
	config := loadComponentConfig(ctx, componentName, toolCtx)
	if len(config) == 0 {
		return "", false
	}
	for _, field := range placeholderedSecretFieldNames(config) {
		for _, qm := range qs {
			if questionTargetsField(qm, field) {
				return field, true
			}
		}
	}
	return "", false
}

func manifestComponentNames(ctx context.Context, toolCtx *tool.Context) []string {
	var names []string
	for _, comp := range manifestComponents(ctx, toolCtx) {
		if name, _ := comp["name"].(string); strings.TrimSpace(name) != "" {
			names = append(names, strings.TrimSpace(name))
		}
	}
	return names
}

func loadComponentConfig(ctx context.Context, componentName string, toolCtx *tool.Context) []byte {
	facade := toolCtx.Facade()
	candidates := []string{componentName + ".json", componentName + ".yaml"}
	if declared := manifestFilenameFor(ctx, componentName, toolCtx); declared != "" {
		candidates = append([]string{declared}, candidates...)
	}
	for _, filename := range candidates {
		data, _, err := facade.LoadArtifact(ctx, filename, nil)
		if err == nil && len(data) > 0 {
			return data
		}
	}
	return nil
}

func manifestFilenameFor(ctx context.Context, componentName string, toolCtx *tool.Context) string {
	for _, comp := range manifestComponents(ctx, toolCtx) {
		name, _ := comp["name"].(string)
		if !strings.EqualFold(strings.TrimSpace(name), componentName) {
			continue
		}
		if filename, _ := comp["filename"].(string); strings.TrimSpace(filename) != "" {
			return strings.TrimSpace(filename)
		}
	}
	return ""
}

func manifestComponents(ctx context.Context, toolCtx *tool.Context) []map[string]any {
	manifest, _, err := toolCtx.Facade().LoadArtifact(ctx, manifestFilename, nil)
	if err != nil || len(manifest) == 0 {
		return nil
	}
	return parseManifestComponents(string(manifest))
}

func placeholderedSecretFieldNames(config []byte) []string {
	var doc any
	if yaml.Unmarshal(config, &doc) != nil {
		return nil
	}
	names := map[string]bool{}
	collectPlaceholderedSecretFields(doc, names)
	fields := make([]string, 0, len(names))
	for name := range names {
		fields = append(fields, name)
	}
	sort.Strings(fields)
	return fields
}

func collectPlaceholderedSecretFields(node any, into map[string]bool) {
	switch v := node.(type) {
	case map[string]any:
		for key, value := range v {
			if s, isString := value.(string); isString {
				if s == samRequiredPlaceholder && isPlatformSecretField(key) {
					into[key] = true
				}
				continue
			}
			collectPlaceholderedSecretFields(value, into)
		}
	case []any:
		for _, item := range v {
			collectPlaceholderedSecretFields(item, into)
		}
	}
}

func isPlatformSecretField(key string) bool {
	_, secret := secretFieldLabels(key)
	return secret
}

func secretFieldLabels(key string) ([]string, bool) {
	labels, secret := secretFieldLabelsLower[strings.ToLower(strings.TrimSpace(key))]
	return labels, secret
}

var secretFieldLabelsLower = mergeLoweredLabels(PlatformSecretFieldLabels, A2AAgentSecretFieldLabels)

func mergeLoweredLabels(maps ...map[string][]string) map[string][]string {
	merged := map[string][]string{}
	for _, m := range maps {
		for name, labels := range m {
			key := strings.ToLower(name)
			merged[key] = appendUnique(merged[key], labels...)
		}
	}
	return merged
}

func appendUnique(into []string, values ...string) []string {
	for _, value := range values {
		if !slices.Contains(into, value) {
			into = append(into, value)
		}
	}
	return into
}

func questionTargetsField(qm map[string]any, field string) bool {
	text, _ := qm["question"].(string)
	placeholder, _ := qm["placeholder"].(string)
	for _, phrase := range fieldPhrases(field) {
		if containsPhrase(text, phrase) || containsPhrase(placeholder, phrase) {
			return true
		}
	}
	return false
}

func fieldPhrases(field string) []string {
	var phrases []string
	if whole := normaliseWords(field); whole != "" {
		phrases = append(phrases, whole)
	}
	labels, _ := secretFieldLabels(field)
	for _, label := range labels {
		phrases = appendUnique(phrases, labelPhrases(label)...)
	}
	return phrases
}

var genericValueSuffixes = map[string]bool{"value": true, "id": true}

const minPhraseWords = 2

func labelPhrases(label string) []string {
	words := strings.Fields(normaliseWords(label))
	if len(words) == 0 {
		return nil
	}
	var phrases []string
	for _, kept := range withoutGenericValueSuffix(words) {
		phrases = appendUnique(phrases, withoutLeadingQualifiers(kept)...)
	}
	return phrases
}

func withoutGenericValueSuffix(words []string) [][]string {
	variants := [][]string{words}
	for len(words) > minPhraseWords && genericValueSuffixes[words[len(words)-1]] {
		words = words[:len(words)-1]
		variants = append(variants, words)
	}
	return variants
}

func withoutLeadingQualifiers(words []string) []string {
	phrases := []string{strings.Join(words, " ")}
	for len(words) > minPhraseWords {
		words = words[1:]
		if genericValueSuffixes[words[len(words)-1]] {
			continue
		}
		phrases = append(phrases, strings.Join(words, " "))
	}
	return phrases
}

func containsPhrase(text, phrase string) bool {
	return strings.Contains(" "+normaliseWords(text)+" ", " "+phrase+" ")
}

func normaliseWords(s string) string {
	var b strings.Builder
	spaced := false
	runes := []rune(s)
	for i, r := range runes {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if !spaced && b.Len() > 0 && startsCamelHump(runes, i) {
				b.WriteByte(' ')
			}
			b.WriteRune(unicode.ToLower(r))
			spaced = false
			continue
		}
		if !spaced && b.Len() > 0 {
			b.WriteByte(' ')
			spaced = true
		}
	}
	return strings.TrimSpace(b.String())
}

func startsCamelHump(runes []rune, i int) bool {
	if i == 0 || !unicode.IsUpper(runes[i]) {
		return false
	}
	previous := runes[i-1]
	if unicode.IsLower(previous) || unicode.IsDigit(previous) {
		return true
	}
	return unicode.IsUpper(previous) && i+1 < len(runes) && unicode.IsLower(runes[i+1])
}
