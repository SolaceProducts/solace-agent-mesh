// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strconv"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const resultSignalsKey = "signals"

const (
	maxResultSignals       = 4
	maxResultSignalBytes   = 256 << 10
	maxRAGSourcesPerSignal = 50
	maxSourceURLLength     = 2048
)

var allowedRAGSearchTypes = map[string]bool{"web_search": true}

var ragSourceMetadataKeys = map[string]bool{"title": false, "type": false, "link": true, "imageUrl": true}

const ragSourceFaviconKey = "favicon"

var (
	errSignalsNotArray    = errors.New("signals is not an array")
	errSignalShape        = errors.New("signal is not a {type, data} object")
	errSignalTooLarge     = errors.New("signal exceeds the size limit")
	errSignalType         = errors.New("signal type is not allowed")
	errSignalData         = errors.New("signal data does not match the signal type's schema")
	errSignalNoTurn       = errors.New("no search turn was assigned to this call")
	errSignalTurn         = errors.New("search_turn does not match the turn assigned to this call")
	errSignalSearchType   = errors.New("search_type is not allowed")
	errSignalSourceCount  = errors.New("signal carries too many sources")
	errSignalCitationID   = errors.New("citation id does not belong to the assigned search turn")
	errSignalSourceScheme = errors.New("source URL is not an absolute http or https URL without credentials, of at most 2048 characters")
	errSignalMetadataKey  = errors.New("source metadata key is not allowed")
)

type resultSignal struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func takeResultSignals(raw map[string]any) any {
	if !tool.IsSerializedToolResult(raw) {
		return nil
	}
	signals, ok := raw[resultSignalsKey]
	if !ok {
		return nil
	}
	delete(raw, resultSignalsKey)
	return signals
}

func (t *Tool) drawSearchTurn(toolCtx *tool.Context) *int {
	if toolCtx.SearchTurns == nil || !tool.NumbersSearches(t.cfg.ToolName) {
		return nil
	}
	if toolCtx.ToolCallID != "" {
		if turn, ok := toolCtx.SearchTurns.TakeHeld(toolCtx.ToolCallID); ok {
			return &turn
		}
	}
	turn := toolCtx.SearchTurns.Next(t.cfg.ToolName)
	return &turn
}

func holdSearchTurn(toolCtx *tool.Context, searchTurn *int, result *tool.Result) {
	if searchTurn != nil && toolCtx.ToolCallID != "" && toolauth.IsPendingAuthResult(result) {
		toolCtx.SearchTurns.Hold(toolCtx.ToolCallID, *searchTurn)
	}
}

func (t *Tool) publishResultSignals(toolCtx *tool.Context, raw any, searchTurn *int) {
	if raw == nil {
		return
	}
	logger := t.logger.With("traceID", toolCtx.TraceID, "taskID", toolCtx.ParentTaskID)
	items, ok := raw.([]any)
	if !ok {
		logger.Warn("dropped tool result signals", "reason", errSignalsNotArray.Error())
		return
	}
	if len(items) > maxResultSignals {
		logger.Warn("tool result signals over the count limit; publishing the first ones",
			"count", len(items), "limit", maxResultSignals)
		items = items[:maxResultSignals]
	}
	for i, item := range items {
		signal, err := decodeResultSignal(item, searchTurn)
		if err != nil {
			logger.Warn("dropped tool result signal", "index", i, "reason", err.Error())
			continue
		}
		if toolCtx.SendSignal == nil {
			logger.Debug("tool result signal not published: no signal channel", "index", i)
			continue
		}
		if err := toolCtx.SendSignal(signal); err != nil {
			logger.Warn("tool result signal publish failed", "index", i, "error", err)
		}
	}
}

func decodeResultSignal(item any, searchTurn *int) (any, error) {
	encoded, err := json.Marshal(item)
	if err != nil {
		return nil, errSignalShape
	}
	if len(encoded) > maxResultSignalBytes {
		return nil, errSignalTooLarge
	}
	var signal resultSignal
	if err := decodeStrict(encoded, &signal); err != nil {
		return nil, errSignalShape
	}
	if signal.Type != string(a2a.SignalRAGInfoUpdate) {
		return nil, errSignalType
	}
	return decodeRAGInfoUpdate(signal.Data, searchTurn)
}

func decodeRAGInfoUpdate(data json.RawMessage, searchTurn *int) (a2a.RAGInfoUpdateData, error) {
	var update a2a.RAGInfoUpdateData
	if err := decodeStrict(data, &update); err != nil {
		return update, errSignalData
	}
	if update.Type != "" && update.Type != string(a2a.SignalRAGInfoUpdate) {
		return update, errSignalData
	}
	update.Type = string(a2a.SignalRAGInfoUpdate)
	if searchTurn == nil {
		return update, errSignalNoTurn
	}
	if update.SearchTurn != *searchTurn {
		return update, errSignalTurn
	}
	if !allowedRAGSearchTypes[update.SearchType] {
		return update, errSignalSearchType
	}
	if len(update.Sources) > maxRAGSourcesPerSignal {
		return update, errSignalSourceCount
	}
	citationID := citationIDPattern(*searchTurn)
	for i := range update.Sources {
		if err := checkRAGSource(&update.Sources[i], citationID); err != nil {
			return update, fmt.Errorf("source %d: %w", i, err)
		}
	}
	return update, nil
}

func citationIDPattern(turn int) *regexp.Regexp {
	return regexp.MustCompile(`^(s|img)` + strconv.Itoa(turn) + `r[0-9]+$`)
}

func checkRAGSource(source *a2a.RAGSource, citationID *regexp.Regexp) error {
	if !citationID.MatchString(source.CitationID) {
		return errSignalCitationID
	}
	delete(source.Metadata, ragSourceFaviconKey)
	urls := []string{source.SourceURL, source.URL}
	for _, key := range slices.Sorted(maps.Keys(source.Metadata)) {
		if _, allowed := ragSourceMetadataKeys[key]; !allowed {
			return fmt.Errorf("%w (allowed: title, type, link, imageUrl)", errSignalMetadataKey)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(source.Metadata)) {
		isURL := ragSourceMetadataKeys[key]
		s, isString := source.Metadata[key].(string)
		if !isString {
			return fmt.Errorf("%w: metadata %s is not a string", errSignalData, key)
		}
		if isURL {
			urls = append(urls, s)
		}
	}
	for _, raw := range urls {
		if !isWebURL(raw) {
			return errSignalSourceScheme
		}
	}
	return nil
}

func isWebURL(raw string) bool {
	if raw == "" {
		return true
	}
	if len(raw) > maxSourceURLLength {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Host != "" && u.User == nil && (u.Scheme == "http" || u.Scheme == "https")
}

func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
