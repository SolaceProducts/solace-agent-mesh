// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

var ErrConversationTooShortToCompact = errors.New("conversation too short to summarize")

type CompactionConfig struct {
	Enabled              bool
	CompactionPercentage float64
	SummaryMaxTokens     int
}

type CompactionMetadata struct {
	Summary        string    `json:"summary"`
	CoveredThrough int       `json:"covered_through"`
	Timestamp      time.Time `json:"timestamp"`
}

const compactionMetadataKey = "_compaction"

const maxCompactionRetries = 3

const DefaultSummaryMaxTokens = 5000

const summarizationPrompt = `You are producing a fresh rolling summary of an ongoing conversation. Your output REPLACES any previous summary — it is not an edit of one. Produce the shortest summary that preserves what is still useful for continuing the conversation.

Rules:
- Be as short as possible. Include only what the next turn genuinely needs and omit everything else — do not pad. There is a hard output limit and anything past it is truncated, so spend your length budget on the essentials first.
- If a "Previous summary" section appears below, treat it as a stale draft. Do not copy it verbatim. Aggressively condense it and drop anything the new messages have made irrelevant or moved past.
- Prioritise the most recent activity: newer events get more detail; older context fades to brief mentions or is dropped entirely.
- Preserve concrete facts the next turn might need: names, identifiers, file paths, dates, error codes, decisions reached, and tool results that influence what comes next. Drop chit-chat, repeated phrasing, and resolved threads.

%s

Output only the new summary text — no preamble, no commentary.`

const (
	msgSummarizationExhausted = taxonomy.SummarizationExhaustedMessage

	msgSummarizationFailed = taxonomy.HistoryTooLongPrefix +
		"We could not further summarize your conversation history. " +
		taxonomy.StartNewConversationSuffix
)

const compactionNotice = "Your conversation history reached the limit. We automatically summarized older messages to keep things running smoothly. " +
	"All important context is preserved, so you can keep chatting seamlessly. Alternatively, you can start a new chat for a fresh conversation."

func getCompactionMetadata(sess *session.Session) *CompactionMetadata {
	if sess.Metadata == nil {
		return nil
	}
	raw, ok := sess.Metadata[compactionMetadataKey]
	if !ok {
		return nil
	}

	switch v := raw.(type) {
	case *CompactionMetadata:
		return v
	case CompactionMetadata:
		return &v
	case map[string]any:
		cm := &CompactionMetadata{}
		if s, ok := v["summary"].(string); ok {
			cm.Summary = s
		}
		if ct, ok := v["covered_through"].(float64); ok {
			cm.CoveredThrough = int(ct)
		} else if ct, ok := v["covered_through"].(int); ok {
			cm.CoveredThrough = ct
		}
		if ts, ok := v["timestamp"].(string); ok {
			cm.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
		}
		return cm
	default:
		data, err := json.Marshal(raw)
		if err != nil {
			return nil
		}
		cm := &CompactionMetadata{}
		if err := json.Unmarshal(data, cm); err != nil {
			return nil
		}
		return cm
	}
}

func setCompactionMetadata(sess *session.Session, cm *CompactionMetadata) {
	if sess.Metadata == nil {
		sess.Metadata = make(map[string]any)
	}
	sess.Metadata[compactionMetadataKey] = cm
}

func filterCompactedMessages(messages []session.Message, compaction *CompactionMetadata) []session.Message {
	if compaction == nil || compaction.CoveredThrough <= 0 || compaction.CoveredThrough > len(messages) {
		return messages
	}

	filtered := make([]session.Message, 0, 1+len(messages)-compaction.CoveredThrough)
	filtered = append(filtered, session.Message{
		Role:      "assistant",
		Content:   "[Conversation Summary]\n" + compaction.Summary,
		Timestamp: compaction.Timestamp,
	})
	filtered = append(filtered, messages[compaction.CoveredThrough:]...)
	return filtered
}

func estimateTokens(msg session.Message) int {
	tokens := (len(msg.Content) + len(msg.Reasoning)) / spend.BytesPerEstimatedToken
	for _, tc := range msg.ToolCalls {
		tokens += len(tc.Arguments) / spend.BytesPerEstimatedToken
		tokens += len(tc.Name) / spend.BytesPerEstimatedToken
	}
	if msg.ToolCallID != "" {
		tokens += len(msg.Content) / spend.BytesPerEstimatedToken
	}
	if tokens == 0 {
		tokens = 1
	}
	return tokens
}

func findCompactionCutoff(messages []session.Message, percentage float64, prefixTokens int) (cutoffIdx int, err error) {
	var userIndices []int
	for i, m := range messages {
		if m.Role == "user" {
			userIndices = append(userIndices, i)
		}
	}

	var boundaries []int
	if len(userIndices) >= 2 {
		boundaries = userIndices[1:]
	} else {
		for i, m := range messages {
			if i >= 2 && m.Role == "assistant" {
				boundaries = append(boundaries, i)
			}
		}
		if len(boundaries) == 0 {
			return 0, fmt.Errorf("%w — need at least 2 user turns or one completed tool round, have %d user turns", ErrConversationTooShortToCompact, len(userIndices))
		}
	}

	totalTokens := prefixTokens
	for _, m := range messages {
		totalTokens += estimateTokens(m)
	}
	targetTokens := int(float64(totalTokens) * percentage)

	cumulative := make([]int, len(messages)+1)
	cumulative[0] = prefixTokens
	for i, m := range messages {
		cumulative[i+1] = cumulative[i] + estimateTokens(m)
	}

	bestCutoff := 0
	bestDistance := totalTokens

	for _, cutoff := range boundaries {
		tokenCount := cumulative[cutoff]
		distance := tokenCount - targetTokens
		if distance < 0 {
			distance = -distance
		}
		if distance < bestDistance {
			bestDistance = distance
			bestCutoff = cutoff
		}
	}

	if bestCutoff == 0 {
		return 0, ErrConversationTooShortToCompact
	}

	return bestCutoff, nil
}

func buildSummarizationPrompt(messages []session.Message, cutoffIdx int, prevCompaction *CompactionMetadata) string {
	var sb strings.Builder

	if prevCompaction != nil && prevCompaction.Summary != "" {
		sb.WriteString("Previous summary (stale — to be condensed and replaced):\n")
		sb.WriteString(prevCompaction.Summary)
		sb.WriteString("\n\nNew messages since the previous summary:\n")
	} else {
		sb.WriteString("Conversation:\n")
	}

	for i := 0; i < cutoffIdx && i < len(messages); i++ {
		m := messages[i]
		role := m.Role
		if role == "assistant" {
			role = "model"
		}
		if role == "tool" {
			fmt.Fprintf(&sb, "tool (%s): %s\n\n", m.ToolCallID, truncateForSummary(m.Content, 200))
			continue
		}
		sb.WriteString(role)
		sb.WriteString(": ")
		sb.WriteString(m.Content)
		if len(m.ToolCalls) > 0 {
			fmt.Fprintf(&sb, " [called %d tools]", len(m.ToolCalls))
		}
		sb.WriteString("\n\n")
	}

	return fmt.Sprintf(summarizationPrompt, sb.String())
}

func truncateForSummary(s string, maxLen int) string {
	return strutil.Truncate(s, maxLen, "...")
}

type CompactionResult struct {
	Metadata *CompactionMetadata

	SummarizerUsage *llm.Usage

	SummarizedMessagesCount int

	TokensFreed int
}

func (a *Agent) performCompaction(ctx context.Context, sess *session.Session, cfg CompactionConfig, spendAttribution *llm.SpendAttribution) (*CompactionResult, error) {
	messages := sess.Messages
	prevCompaction := getCompactionMetadata(sess)

	startIdx := 0
	if prevCompaction != nil {
		startIdx = prevCompaction.CoveredThrough
	}
	workingMessages := messages[startIdx:]

	prefixTokens := 0
	if prevCompaction != nil {
		prefixTokens = len(prevCompaction.Summary) / spend.BytesPerEstimatedToken
	}

	cutoffIdx, err := findCompactionCutoff(workingMessages, cfg.CompactionPercentage, prefixTokens)
	if err != nil {
		return nil, err
	}

	prompt := buildSummarizationPrompt(workingMessages, cutoffIdx, prevCompaction)

	maxTokens := cfg.SummaryMaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultSummaryMaxTokens
	}

	resp, err := a.llmClient(ctx).ChatCompletion(ctx, &llm.ChatRequest{
		Model:     a.cfg.Model,
		MaxTokens: maxTokens,
		Messages: []llm.ChatMessage{
			{Role: "user", Content: prompt},
		},
		Spend: spendAttribution,
	})
	if err != nil {
		return nil, fmt.Errorf("summarization LLM call failed: %w", err)
	}

	summary := resp.Content
	if summary == "" {
		return nil, fmt.Errorf("summarization LLM returned empty response")
	}

	if resp.FinishReason == "length" || resp.FinishReason == "max_tokens" {
		a.logger.Warn("summarization output truncated by max_tokens — summary may be cut mid-sentence; raise auto_summarization.summary_max_tokens",
			"finishReason", resp.FinishReason,
			"maxTokens", maxTokens,
			"summaryLength", len(summary),
		)
	}

	absoluteCutoff := startIdx + cutoffIdx

	cm := &CompactionMetadata{
		Summary:        summary,
		CoveredThrough: absoluteCutoff,
		Timestamp:      time.Now(),
	}
	setCompactionMetadata(sess, cm)

	tokensFreed := 0
	for i := startIdx; i < absoluteCutoff && i < len(messages); i++ {
		tokensFreed += estimateTokens(messages[i])
	}

	a.logger.Info("context compaction completed",
		"coveredThrough", absoluteCutoff,
		"totalMessages", len(messages),
		"summaryLength", len(summary),
	)

	return &CompactionResult{
		Metadata:                cm,
		SummarizerUsage:         resp.Usage,
		SummarizedMessagesCount: absoluteCutoff - startIdx,
		TokensFreed:             tokensFreed,
	}, nil
}

const currentTimePrefix = "[Current time: "

func currentTimeStamp(now time.Time, tzName string) (stamp string, tzFallback bool) {
	loc, name, fallback := userLocation(tzName)
	return renderTimeStamp(now.In(loc), name), fallback
}

func userLocation(tzName string) (loc *time.Location, name string, fallback bool) {
	if tzName == "" {
		return time.UTC, "UTC", false
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return time.UTC, "UTC", true
	}
	return loc, tzName, false
}

func renderTimeStamp(t time.Time, tzName string) string {
	return t.Format("Monday, ") + t.Format(time.RFC3339) + " (" + tzName + ")"
}

func stripCurrentTimeStamp(s string) string {
	rest, ok := strings.CutPrefix(s, currentTimePrefix)
	if !ok {
		return s
	}
	i := strings.Index(rest, "]\n")
	if i < 0 || !isAgentStampBody(rest[:i]) {
		return s
	}
	return rest[i+2:]
}

func isAgentStampBody(body string) bool {
	if strings.ContainsRune(body, '\n') {
		return false
	}
	if _, err := time.Parse(time.RFC3339, body); err == nil {
		return true
	}
	f := strings.Fields(body)
	if len(f) != 3 {
		return false
	}
	t, err := time.Parse(time.RFC3339, f[1])
	if err != nil {
		return false
	}
	zone := f[2]
	return f[0] == t.Weekday().String()+"," &&
		len(zone) > 2 && strings.HasPrefix(zone, "(") && strings.HasSuffix(zone, ")")
}

func extractLastUserText(messages []llm.ChatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}

const stubbedToolResult = "[This call succeeded and its result was used in the answer that follows. The result itself is left out to save space; call the tool again if you need the raw data.]"

func stubToolResults(history []llm.ChatMessage, batchBytes int) []llm.ChatMessage {
	boundary := toolResultStubBoundary(history, batchBytes)
	stubbed := make([]llm.ChatMessage, len(history))
	start := 0
	for i, m := range history {
		if m.Role == "tool" {
			size := len(m.Content)
			if start < boundary {
				m.Content = stubbedToolResult
				m.ContentBlocks = nil
				m.WithheldBytes = 0
			}
			start += size
		}
		stubbed[i] = m
	}
	return stubbed
}

func toolResultStubBoundary(history []llm.ChatMessage, batchBytes int) int {
	if batchBytes <= 0 {
		return math.MaxInt
	}
	total := 0
	for _, m := range history {
		if m.Role == "tool" {
			total += len(m.Content)
		}
	}
	return total / batchBytes * batchBytes
}
