// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"cmp"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/core/providers/anthropic"
	"github.com/maximhq/bifrost/core/schemas"
)

type RequestDefaults struct {
	Temperature       *float64
	MaxTokens         int
	StopSequences     []string
	ParallelToolCalls *bool
	ExtraParams       map[string]any
}

type storedParam struct {
	name  string
	words []string
}

var (
	storedTemperature       = storedParam{name: "temperature", words: []string{"temperature"}}
	storedOutputLimit       = storedParam{name: "max_tokens", words: []string{"maxtokens", "maxcompletiontokens", "maxoutputtokens"}}
	storedStopSequences     = storedParam{name: "stop", words: []string{"stop", "stopsequences"}}
	storedParallelToolCalls = storedParam{name: "parallel_tool_calls", words: []string{"paralleltoolcalls"}}
)

func storedExtraParam(key string) storedParam {
	return storedParam{name: key, words: []string{errorWord(key)}}
}

func (c *BifrostClient) withRequestDefaults(req *ChatRequest) *ChatRequest {
	return c.withRequestDefaultsExcept(req, nil)
}

func (c *BifrostClient) withRequestDefaultsExcept(req *ChatRequest, omit []string) *ChatRequest {
	if !req.WithStoredParams {
		return req
	}
	stored := c.cfg.RequestDefaults
	omitted := func(name string) bool { return slices.Contains(omit, name) || c.storedParamDropped(name, req) }
	addsTemperature := stored.Temperature != nil && req.Temperature == nil && !omitted(storedTemperature.name)
	addsMaxTokens := stored.MaxTokens > 0 && req.MaxTokens == 0 && !omitted(storedOutputLimit.name)
	addsStop := len(stored.StopSequences) > 0 && !omitted(storedStopSequences.name)
	addsParallelToolCalls := stored.ParallelToolCalls != nil && req.ParallelToolCalls == nil && !omitted(storedParallelToolCalls.name)
	extras := maps.Clone(stored.ExtraParams)
	maps.DeleteFunc(extras, func(key string, _ any) bool { return omitted(key) })
	if !addsTemperature && !addsMaxTokens && !addsStop && !addsParallelToolCalls && len(extras) == 0 {
		return req
	}

	sent := *req
	if addsTemperature {
		sent.Temperature = new(*stored.Temperature)
	}
	if addsMaxTokens {
		sent.MaxTokens = c.outputLimitBeside(stored.MaxTokens, req)
	}
	if addsStop {
		sent.StopSequences = withStoredStops(req.StopSequences, stored.StopSequences)
	}
	if addsParallelToolCalls {
		sent.ParallelToolCalls = new(*stored.ParallelToolCalls)
	}
	if len(extras) > 0 {
		maps.Copy(extras, req.ExtraParams)
		sent.ExtraParams = extras
	}
	return &sent
}

func (c *BifrostClient) outputLimitBeside(limit int, req *ChatRequest) int {
	reasoning := buildReasoningParams(req.Reasoning)
	switch {
	case reasoning == nil:
		return limit
	case reasoning.MaxTokens != nil && *reasoning.MaxTokens >= limit:
		return *reasoning.MaxTokens + limit
	case reasoning.Effort != nil && *reasoning.Effort != "none" &&
		limit <= anthropic.MinimumReasoningMaxTokens && c.sizesClaudeThinkingFromTheLimit(req):
		return limit + anthropic.MinimumReasoningMaxTokens
	}
	return limit
}

func (c *BifrostClient) sizesClaudeThinkingFromTheLimit(req *ChatRequest) bool {
	return fillsClaudeMaxTokensFromBifrostTable(c.cfg.Provider) && IsClaudeModel(cmp.Or(req.Model, c.cfg.Model))
}

func withStoredStops(own, stored []string) []string {
	stops := slices.Clone(own)
	for _, stop := range stored {
		if !slices.Contains(stops, stop) {
			stops = append(stops, stop)
		}
	}
	return stops
}

type droppedStoredParam struct {
	name          string
	whileThinking bool
}

func (c *BifrostClient) storedParamDropped(name string, req *ChatRequest) bool {
	if _, dropped := c.droppedStoredParams.Load(droppedStoredParam{name: name}); dropped {
		return true
	}
	_, droppedWhileThinking := c.droppedStoredParams.Load(droppedStoredParam{name: name, whileThinking: true})
	return droppedWhileThinking && thinks(req)
}

func (c *BifrostClient) rememberDroppedStoredParam(name string, whileThinking bool) bool {
	_, known := c.droppedStoredParams.LoadOrStore(droppedStoredParam{name: name, whileThinking: whileThinking}, struct{}{})
	return !known
}

func thinks(req *ChatRequest) bool {
	return buildReasoningParams(req.Reasoning) != nil
}

func (c *BifrostClient) dropRejectedStoredTemperature(req, sent *ChatRequest, err error) bool {
	if c.cfg.StrictRequestDefaults || req.Temperature != nil || sent.Temperature == nil {
		return false
	}
	llmErr, ok := errors.AsType[*Error](err)
	if !ok || !llmErr.IsTemperatureUnsupportedError() {
		return false
	}
	if thinks(sent) && llmErr.refusesTemperatureOnlyWhileThinking() {
		if c.rememberDroppedStoredParam(storedTemperature.name, true) {
			c.logger.Warn("model rejected the temperature stored on its configuration while thinking; omitting it on requests that think",
				"provider", c.cfg.Provider, "model", c.cfg.Model, "temperature", *sent.Temperature)
		}
		return true
	}
	if c.rememberDroppedStoredParam(storedTemperature.name, false) {
		c.logger.Warn("model rejected the temperature stored on its configuration; omitting it",
			"provider", c.cfg.Provider, "model", c.cfg.Model, "temperature", *sent.Temperature)
	}
	return true
}

func redoWithoutRefusedStoredParams[T any](c *BifrostClient, req, sent *ChatRequest, refused error,
	send func(sent *ChatRequest, bfReq *schemas.BifrostChatRequest, priorAttempts int) (T, error),
) (T, bool) {
	var dropped []string
	err := refused
	for {
		llmErr, carried, redoable := c.refusalOfStoredParams(req, sent, err)
		if !redoable {
			var none T
			return none, false
		}
		named := llmErr.namedAmong(carried)
		if len(named) == 0 {
			redone, redoErr := redo(c, req, err, send)
			if redoErr != nil {
				var none T
				return none, false
			}
			c.warnStoredParamsOmittedOnce(carried, llmErr)
			return redone, true
		}
		for _, param := range named {
			dropped = append(dropped, param.name)
		}
		sent = c.withRequestDefaultsExcept(req, dropped)
		redone, redoErr := redo(c, sent, err, send)
		if redoErr == nil {
			c.rememberRefusedStoredParams(dropped, thinks(req), llmErr)
			return redone, true
		}
		err = redoErr
	}
}

func (c *BifrostClient) refusalOfStoredParams(req, sent *ChatRequest, err error) (*Error, []storedParam, bool) {
	if c.cfg.StrictRequestDefaults {
		return nil, nil, false
	}
	carried := storedParamsAddedTo(req, sent)
	llmErr, ok := errors.AsType[*Error](err)
	if len(carried) == 0 || !ok || !llmErr.refusesTheRequestItself() {
		return nil, nil, false
	}
	return llmErr, carried, true
}

func (e *Error) refusesTheRequestItself() bool {
	return e.StatusCode == http.StatusBadRequest && !e.raisedByBifrost && !e.IsContextLimitError() && !e.IsRateLimited()
}

func storedParamsAddedTo(req, sent *ChatRequest) []storedParam {
	var added []storedParam
	if sent.Temperature != nil && req.Temperature == nil {
		added = append(added, storedTemperature)
	}
	if sent.MaxTokens != req.MaxTokens && req.MaxTokens == 0 {
		added = append(added, storedOutputLimit)
	}
	if len(sent.StopSequences) > len(req.StopSequences) {
		added = append(added, storedStopSequences)
	}
	if sent.ParallelToolCalls != nil && req.ParallelToolCalls == nil {
		added = append(added, storedParallelToolCalls)
	}
	for _, key := range slices.Sorted(maps.Keys(sent.ExtraParams)) {
		if _, own := req.ExtraParams[key]; !own {
			added = append(added, storedExtraParam(key))
		}
	}
	return added
}

var errorWords = regexp.MustCompile(`[A-Za-z0-9_-]+`)

func errorWord(text string) string {
	return strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(text))
}

func (e *Error) namedAmong(params []storedParam) []storedParam {
	spoken := make(map[string]bool)
	for _, word := range errorWords.FindAllString(e.Message, -1) {
		spoken[errorWord(word)] = true
	}
	return slices.DeleteFunc(slices.Clone(params), func(p storedParam) bool {
		return !slices.ContainsFunc(p.words, func(word string) bool { return spoken[word] })
	})
}

func (c *BifrostClient) rememberRefusedStoredParams(names []string, whileThinking bool, refusal *Error) {
	for _, name := range names {
		if c.rememberDroppedStoredParam(name, whileThinking) {
			c.logger.Warn("model refused a parameter stored on its configuration; omitting it",
				"provider", c.cfg.Provider, "model", c.cfg.Model, "storedParam", name, "whileThinking", whileThinking,
				"httpStatus", refusal.StatusCode, "errorType", refusal.Type, "errorCode", refusal.Code)
		}
	}
}

func (c *BifrostClient) warnStoredParamsOmittedOnce(carried []storedParam, refusal *Error) {
	names := make([]string, len(carried))
	for i, param := range carried {
		names[i] = param.name
	}
	c.logger.Warn("model refused a request carrying the parameters stored on its configuration and its error named none of them; sent it without them this once",
		"provider", c.cfg.Provider, "model", c.cfg.Model, "storedParams", names,
		"httpStatus", refusal.StatusCode, "errorType", refusal.Type, "errorCode", refusal.Code)
}

func IntParam(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), n == math.Trunc(n) && !math.IsInf(n, 0)
	case float32:
		return IntParam(float64(n))
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	}
	return 0, false
}

func FloatParam(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func (c *BifrostClient) warnStoredParamsOffResponses(dropped []string) {
	stored := c.cfg.RequestDefaults
	for _, key := range dropped {
		if _, isStored := stored.ExtraParams[key]; isStored {
			c.warnStoredParamNotSent(key)
		}
	}
	if len(stored.StopSequences) > 0 {
		c.warnStoredParamNotSent("stop")
	}
}

func (c *BifrostClient) warnStoredParamNotSent(key string) {
	if _, logged := c.storedParamsNotSentLogged.LoadOrStore(key, struct{}{}); !logged {
		c.logger.Warn("stored model parameter is not sent on the OpenAI Responses API",
			"provider", c.cfg.Provider, "model", c.cfg.Model, "key", key)
	}
}

const azureAPIVersionKey = "api_version"

var providerConfigKeys = sync.OnceValue(func() map[schemas.ModelProvider]map[string]bool {
	azure := jsonFieldNames(schemas.AzureKeyConfig{})
	azure[azureAPIVersionKey] = true
	azure[azureChatRouteKey] = true
	for _, credential := range azureCredentialKeys {
		azure[credential] = true
	}
	vertex := jsonFieldNames(schemas.VertexKeyConfig{})
	for litellmName := range vertexKeyRename {
		vertex[litellmName] = true
	}
	bedrock := jsonFieldNames(schemas.BedrockKeyConfig{})
	for litellmName := range bedrockKeyRename {
		bedrock[litellmName] = true
	}
	return map[schemas.ModelProvider]map[string]bool{
		schemas.Azure:   azure,
		schemas.Vertex:  vertex,
		schemas.Bedrock: bedrock,
		schemas.Ollama:  jsonFieldNames(schemas.OllamaKeyConfig{}),
	}
})

func IsProviderConfigKey(provider, key string) bool {
	keys := providerConfigKeys()[schemas.ModelProvider(provider)]
	return keys[key] || keys[strings.TrimSuffix(key, "_file")]
}

func jsonFieldNames(keyConfig any) map[string]bool {
	t := reflect.TypeOf(keyConfig)
	names := make(map[string]bool, t.NumField())
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			names[name] = true
		}
	}
	return names
}
