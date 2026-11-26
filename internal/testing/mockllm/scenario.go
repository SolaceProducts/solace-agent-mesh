// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockllm

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
)

type Scenario struct {
	ID          string            `json:"id"`
	Description string            `json:"description,omitempty"`
	Responses   []json.RawMessage `json:"responses"`

	Match string `json:"match,omitempty"`

	Precomputed []PrecomputedResponse `json:"-"`

	matcher *regexp.Regexp
}

func (s *Scenario) Matcher() *regexp.Regexp { return s.matcher }

type PrecomputedResponse struct {
	FullJSON []byte

	StreamFrames [][]byte

	TTFT *time.Duration

	StreamDelay *time.Duration

	HTTPError *HTTPErrorResponse
}

type HTTPErrorResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body"`
}

type ResponseLatency struct {
	TTFTMs        *int `json:"ttftMs"`
	StreamDelayMs *int `json:"streamDelayMs"`
	ChunkChars    *int `json:"chunkChars"`
}

type StreamFault struct {
	OmitFinishReason bool `json:"omitFinishReason"`
}

type ChatCompletionResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
}

type Choice struct {
	Index        int             `json:"index"`
	Message      ResponseMessage `json:"message"`
	FinishReason string          `json:"finish_reason"`
}

type ResponseMessage struct {
	Role             string     `json:"role"`
	Content          *string    `json:"content"`
	ReasoningContent *string    `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	Index    *int         `json:"index,omitempty"`
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CachedTokens     int `json:"cached_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
	ReasoningTokens  int `json:"reasoning_tokens"`
}

type usageWire struct {
	PromptTokens            int                      `json:"prompt_tokens"`
	CompletionTokens        int                      `json:"completion_tokens"`
	TotalTokens             int                      `json:"total_tokens"`
	PromptTokensDetails     *promptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *completionTokensDetails `json:"completion_tokens_details,omitempty"`
}

type promptTokensDetails struct {
	CachedTokens     int `json:"cached_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
}

type completionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

func (u Usage) MarshalJSON() ([]byte, error) {
	if u.CacheWriteTokens > u.CachedTokens {
		return nil, fmt.Errorf("usage: cache_write_tokens %d exceeds cached_tokens %d, which include them", u.CacheWriteTokens, u.CachedTokens)
	}
	wire := usageWire{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
	}
	if u.CachedTokens > 0 || u.CacheWriteTokens > 0 {
		wire.PromptTokensDetails = &promptTokensDetails{
			CachedTokens:     u.CachedTokens - u.CacheWriteTokens,
			CacheWriteTokens: u.CacheWriteTokens,
		}
	}
	if u.ReasoningTokens > 0 {
		wire.CompletionTokensDetails = &completionTokensDetails{ReasoningTokens: u.ReasoningTokens}
	}
	return json.Marshal(wire)
}

func (u *Usage) UnmarshalJSON(data []byte) error {
	type flat Usage
	var f flat
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	var wire usageWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*u = Usage(f)
	if d := wire.PromptTokensDetails; d != nil {
		u.CachedTokens = d.CachedTokens + d.CacheWriteTokens
		u.CacheWriteTokens = d.CacheWriteTokens
	}
	if d := wire.CompletionTokensDetails; d != nil {
		u.ReasoningTokens = d.ReasoningTokens
	}
	if u.CacheWriteTokens > u.CachedTokens {
		return fmt.Errorf("usage: cache_write_tokens %d exceeds cached_tokens %d, which include them", u.CacheWriteTokens, u.CachedTokens)
	}
	return nil
}

type ChatCompletionChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []ChunkChoice `json:"choices"`
	Usage   *Usage        `json:"usage,omitempty"`
}

type ChunkChoice struct {
	Index        int        `json:"index"`
	Delta        ChunkDelta `json:"delta"`
	FinishReason *string    `json:"finish_reason"`
}

type ChunkDelta struct {
	Role      string     `json:"role,omitempty"`
	Content   string     `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type ChatCompletionRequest struct {
	Model    string           `json:"model"`
	Messages []RequestMessage `json:"messages"`
	Stream   bool             `json:"stream"`
	Tools    json.RawMessage  `json:"tools,omitempty"`
}

type RequestMessage struct {
	Role       string          `json:"role"`
	Content    string          `json:"-"`
	RawContent json.RawMessage `json:"content"`
}

func (m *RequestMessage) UnmarshalJSON(data []byte) error {
	type Alias RequestMessage
	var raw struct {
		Alias
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*m = RequestMessage(raw.Alias)

	if len(raw.Content) == 0 || string(raw.Content) == "null" {
		return nil
	}

	var s string
	if err := json.Unmarshal(raw.Content, &s); err == nil {
		m.Content = s
		return nil
	}

	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw.Content, &parts); err == nil {
		var texts []string
		for _, p := range parts {
			if p.Text != "" {
				texts = append(texts, p.Text)
			}
		}
		m.Content = strings.Join(texts, "\n")
		return nil
	}

	return nil
}

var (
	TestCaseIDRe    = regexp.MustCompile(`\[test_case_id=([\w.\-]+)\]`)
	ResponsesJSONRe = regexp.MustCompile(`\[responses_json=([\w=+/]+)\]`)
	ScenarioTagRe   = regexp.MustCompile(`\[scenario:([\w.\-]+)\]`)
)

func LoadScenario(path string) (*Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read scenario file: %w", err)
	}
	return ParseScenario(data)
}

func ParseScenario(data []byte) (*Scenario, error) {
	var s Scenario
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse scenario: %w", err)
	}
	if err := s.Precompute(); err != nil {
		return nil, fmt.Errorf("precompute scenario %q: %w", s.ID, err)
	}
	return &s, nil
}

func (s *Scenario) Precompute() error {
	s.Precomputed = make([]PrecomputedResponse, len(s.Responses))
	for i, raw := range s.Responses {
		pc, err := PrecomputeResponse(raw, i)
		if err != nil {
			return fmt.Errorf("response %d: %w", i, err)
		}
		s.Precomputed[i] = pc
	}
	if s.Match != "" {
		re, err := regexp.Compile(s.Match)
		if err != nil {
			return fmt.Errorf("compile match regex %q: %w", s.Match, err)
		}
		s.matcher = re
	}
	return nil
}

func PrecomputeResponse(raw json.RawMessage, index int) (PrecomputedResponse, error) {
	return PrecomputeResponseWithChunk(raw, index, StreamChunkChars)
}

func PrecomputeResponseWithChunk(raw json.RawMessage, index, chunkChars int) (PrecomputedResponse, error) {
	var errorEnvelope struct {
		HTTPError *HTTPErrorResponse `json:"httpError"`
	}
	if err := json.Unmarshal(raw, &errorEnvelope); err != nil {
		return PrecomputedResponse{}, fmt.Errorf("parse httpError: %w", err)
	}
	if errorEnvelope.HTTPError != nil {
		if errorEnvelope.HTTPError.Status < 400 || errorEnvelope.HTTPError.Status > 599 {
			return PrecomputedResponse{}, fmt.Errorf("httpError.status = %d, want a 4xx/5xx", errorEnvelope.HTTPError.Status)
		}
		return PrecomputedResponse{HTTPError: errorEnvelope.HTTPError}, nil
	}

	var resp ChatCompletionResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return PrecomputedResponse{}, fmt.Errorf("parse response: %w", err)
	}

	if resp.ID == "" {
		resp.ID = fmt.Sprintf("chatcmpl-perf-%d", index)
	}
	if resp.Object == "" {
		resp.Object = "chat.completion"
	}
	if resp.Model == "" {
		resp.Model = "perf-test-model"
	}
	if len(resp.Choices) > 0 && resp.Choices[0].Message.Role == "" {
		resp.Choices[0].Message.Role = "assistant"
	}

	var faultEnvelope struct {
		StreamFault *StreamFault `json:"streamFault"`
	}
	if err := json.Unmarshal(raw, &faultEnvelope); err != nil {
		return PrecomputedResponse{}, fmt.Errorf("parse streamFault: %w", err)
	}
	omitFinishReason := faultEnvelope.StreamFault != nil && faultEnvelope.StreamFault.OmitFinishReason
	if omitFinishReason && len(resp.Choices) == 0 {
		return PrecomputedResponse{}, errors.New("streamFault.omitFinishReason needs at least one choice to strip the finish reason from")
	}
	for i := range resp.Choices {
		if resp.Choices[i].FinishReason == "" {
			resp.Choices[i].FinishReason = defaultFinishReason(resp.Choices[i].Message)
		}
	}

	fullJSON, err := json.Marshal(resp)
	if err != nil {
		return PrecomputedResponse{}, fmt.Errorf("marshal full response: %w", err)
	}

	var latencyEnvelope struct {
		Latency *ResponseLatency `json:"latency"`
	}
	if err := json.Unmarshal(raw, &latencyEnvelope); err != nil {
		return PrecomputedResponse{}, fmt.Errorf("parse latency: %w", err)
	}

	var ttft, streamDelay *time.Duration
	if lat := latencyEnvelope.Latency; lat != nil {
		if lat.ChunkChars != nil {
			chunkChars = *lat.ChunkChars
		}
		if lat.TTFTMs != nil {
			ttft = new(time.Duration(*lat.TTFTMs) * time.Millisecond)
		}
		if lat.StreamDelayMs != nil {
			streamDelay = new(time.Duration(*lat.StreamDelayMs) * time.Millisecond)
		}
	}

	streamed := resp
	if omitFinishReason {
		streamed = withoutFinishReasons(resp)
	}
	frames := BuildStreamFramesWithChunk(streamed, chunkChars)

	return PrecomputedResponse{
		FullJSON:     fullJSON,
		StreamFrames: frames,
		TTFT:         ttft,
		StreamDelay:  streamDelay,
	}, nil
}

func withoutFinishReasons(resp ChatCompletionResponse) ChatCompletionResponse {
	resp.Choices = slices.Clone(resp.Choices)
	for i := range resp.Choices {
		resp.Choices[i].FinishReason = ""
	}
	return resp
}

func defaultFinishReason(msg ResponseMessage) string {
	if len(msg.ToolCalls) > 0 {
		return "tool_calls"
	}
	return "stop"
}

var StreamChunkChars = 0

func BuildStreamFrames(resp ChatCompletionResponse) [][]byte {
	return BuildStreamFramesWithChunk(resp, StreamChunkChars)
}

func BuildStreamFramesWithChunk(resp ChatCompletionResponse, chunkChars int) [][]byte {
	var frames [][]byte

	if len(resp.Choices) == 0 {
		frames = append(frames, SSEFrame(ChatCompletionChunk{
			ID: resp.ID, Object: "chat.completion.chunk", Model: resp.Model,
			Choices: []ChunkChoice{{Index: 0, Delta: ChunkDelta{Role: "assistant"}}},
		}))
		frames = append(frames, SSEFrame(ChatCompletionChunk{
			ID: resp.ID, Object: "chat.completion.chunk", Model: resp.Model,
			Choices: []ChunkChoice{{Index: 0, FinishReason: new("stop")}},
			Usage:   resp.Usage,
		}))
		frames = append(frames, []byte("data: [DONE]\n\n"))
		return frames
	}

	choice := resp.Choices[0]
	msg := choice.Message

	frames = append(frames, SSEFrame(ChatCompletionChunk{
		ID: resp.ID, Object: "chat.completion.chunk", Model: resp.Model,
		Choices: []ChunkChoice{{Index: 0, Delta: ChunkDelta{Role: "assistant"}}},
	}))

	if msg.Content != nil && *msg.Content != "" {
		var chunks []string
		if chunkChars > 0 {
			chunks = SplitContentBySize(*msg.Content, chunkChars)
		} else {
			chunks = SplitContent(*msg.Content, 3)
		}
		for _, chunk := range chunks {
			frames = append(frames, SSEFrame(ChatCompletionChunk{
				ID: resp.ID, Object: "chat.completion.chunk", Model: resp.Model,
				Choices: []ChunkChoice{{Index: 0, Delta: ChunkDelta{Content: chunk}}},
			}))
		}
	}

	for i, tc := range msg.ToolCalls {
		idx := i
		if tc.Index != nil {
			idx = *tc.Index
		}
		frames = append(frames, SSEFrame(ChatCompletionChunk{
			ID: resp.ID, Object: "chat.completion.chunk", Model: resp.Model,
			Choices: []ChunkChoice{{Index: 0, Delta: ChunkDelta{
				ToolCalls: []ToolCall{{
					Index: &idx,
					ID:    tc.ID,
					Type:  "function",
					Function: FunctionCall{
						Name:      tc.Function.Name,
						Arguments: "",
					},
				}},
			}}},
		}))
		frames = append(frames, SSEFrame(ChatCompletionChunk{
			ID: resp.ID, Object: "chat.completion.chunk", Model: resp.Model,
			Choices: []ChunkChoice{{Index: 0, Delta: ChunkDelta{
				ToolCalls: []ToolCall{{
					Index:    &idx,
					Function: FunctionCall{Arguments: tc.Function.Arguments},
				}},
			}}},
		}))
	}

	var finishReason *string
	if choice.FinishReason != "" {
		finishReason = new(choice.FinishReason)
	}
	frames = append(frames, SSEFrame(ChatCompletionChunk{
		ID: resp.ID, Object: "chat.completion.chunk", Model: resp.Model,
		Choices: []ChunkChoice{{Index: 0, FinishReason: finishReason}},
		Usage:   resp.Usage,
	}))

	frames = append(frames, []byte("data: [DONE]\n\n"))

	return frames
}

func SSEFrame(chunk ChatCompletionChunk) []byte {
	data, _ := json.Marshal(chunk)
	var buf []byte
	buf = append(buf, "data: "...)
	buf = append(buf, data...)
	buf = append(buf, "\n\n"...)
	return buf
}

func SplitContent(text string, n int) []string {
	if n <= 0 {
		n = 1
	}
	if len(text) <= n {
		return []string{text}
	}
	chunkSize := (len(text) + n - 1) / n
	var chunks []string
	for i := 0; i < len(text); i += chunkSize {
		end := i + chunkSize
		if end > len(text) {
			end = len(text)
		}
		chunks = append(chunks, text[i:end])
	}
	return chunks
}

func SplitContentBySize(text string, size int) []string {
	if size <= 0 {
		return []string{text}
	}
	runes := []rune(text)
	if len(runes) <= size {
		return []string{text}
	}
	chunks := make([]string, 0, (len(runes)+size-1)/size)
	for i := 0; i < len(runes); i += size {
		end := i + size
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[i:end]))
	}
	return chunks
}

func (s *Scenario) EncodeResponsesJSON() (string, error) {
	data, err := json.Marshal(s.Responses)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func ExtractScenarioFromMessages(messages []RequestMessage) (scenarioID string, inlineResponses []json.RawMessage, turnIndex int) {
	for _, msg := range messages {
		if msg.Role == "assistant" {
			turnIndex++
		}
	}

	for _, msg := range messages {
		if m := TestCaseIDRe.FindStringSubmatch(msg.Content); len(m) > 1 {
			scenarioID = m[1]
		}
		if m := ResponsesJSONRe.FindStringSubmatch(msg.Content); len(m) > 1 {
			decoded, err := base64.StdEncoding.DecodeString(m[1])
			if err == nil {
				_ = json.Unmarshal(decoded, &inlineResponses)
			}
		}
		if m := ScenarioTagRe.FindStringSubmatch(msg.Content); len(m) > 1 {
			scenarioID = m[1]
		}
	}
	return
}

func StripScenarioTag(text string) string {
	loc := ScenarioTagRe.FindStringIndex(text)
	if loc == nil {
		return text
	}
	return strings.TrimSpace(text[:loc[0]] + text[loc[1]:])
}
