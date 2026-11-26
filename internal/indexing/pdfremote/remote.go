// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pdfremote

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/indexing/convert"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
)

const ToolName = "pdf_extract_text"

const latestArtifactVersion = -1

type Extractor struct {
	executor       *remote.Executor
	appName        string
	userID         string
	sessionID      string
	authToken      string
	timeoutSeconds int
	corrIDPrefix   string
	corrIDCounter  atomic.Uint64
	logger         *slog.Logger
}

type Config struct {
	Executor *remote.Executor

	AppName   string
	UserID    string
	SessionID string

	AuthToken string

	TimeoutSeconds int

	CorrIDPrefix string

	Logger *slog.Logger
}

func New(cfg Config) *Extractor {
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 120
	}
	if cfg.CorrIDPrefix == "" {
		cfg.CorrIDPrefix = "pdf-extract"
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Extractor{
		executor:       cfg.Executor,
		appName:        cfg.AppName,
		userID:         cfg.UserID,
		sessionID:      cfg.SessionID,
		authToken:      cfg.AuthToken,
		timeoutSeconds: cfg.TimeoutSeconds,
		corrIDPrefix:   cfg.CorrIDPrefix,
		logger:         logger.With("logger_name", "pdf-remote-extractor"),
	}
}

func (e *Extractor) Extract(ctx context.Context, filename string, _ []byte) (*convert.Result, error) {
	if e == nil || e.executor == nil {
		return nil, fmt.Errorf("pdfremote: executor not configured")
	}

	corrID := e.nextCorrID()

	req := &remote.InvokeRequest{
		ToolName: ToolName,
		Args:     map[string]any{},
		ArtifactReferences: map[string]remotetool.ArtifactReference{
			"input_document": {Filename: filename, Version: latestArtifactVersion},
		},
		AppName:        e.appName,
		UserID:         e.userID,
		SessionID:      e.sessionID,
		AuthToken:      e.authToken,
		TimeoutSeconds: e.timeoutSeconds,
	}

	resp, err := e.executor.Invoke(ctx, corrID, req, nil)
	if err != nil {
		return nil, fmt.Errorf("pdf_extract_text invoke: %w", err)
	}
	return interpretToolResult(resp)
}

func interpretToolResult(resp *remote.InvokeResponse) (*convert.Result, error) {
	if resp == nil || resp.Result.ToolResult == nil {
		return &convert.Result{Text: "", CitationType: "page"}, nil
	}

	if status, _ := resp.Result.ToolResult["status"].(string); status == "error" {
		msg, _ := resp.Result.ToolResult["message"].(string)
		if msg == "" {
			msg = "tool returned error result"
		}
		return nil, fmt.Errorf("pdf_extract_text: %s", msg)
	}

	pages := extractPagesField(resp.Result.ToolResult)
	return mapPagesToResult(pages), nil
}

var globalCorrIDCounter atomic.Uint64

func (e *Extractor) nextCorrID() string {
	return e.corrIDPrefix + "-" +
		strconv.FormatInt(time.Now().UnixNano(), 10) + "-" +
		strconv.FormatUint(globalCorrIDCounter.Add(1), 10) + "-" +
		strconv.FormatUint(e.corrIDCounter.Add(1), 10)
}

func extractPagesField(toolResult map[string]any) []any {
	if data, ok := toolResult["data"].(map[string]any); ok {
		if pages, ok := data["pages"].([]any); ok {
			return pages
		}
	}
	if pages, ok := toolResult["pages"].([]any); ok {
		return pages
	}
	return nil
}

func mapPagesToResult(pages []any) *convert.Result {
	if len(pages) == 0 {
		return &convert.Result{Text: "", CitationType: "page"}
	}

	type entry struct {
		number int
		text   string
	}
	parsed := make([]entry, 0, len(pages))
	for _, p := range pages {
		m, ok := p.(map[string]any)
		if !ok {
			continue
		}
		text, _ := m["text"].(string)
		if text == "" {
			continue
		}
		num := 0
		switch n := m["number"].(type) {
		case float64:
			num = int(n)
		case int:
			num = n
		}
		parsed = append(parsed, entry{number: num, text: text})
	}

	if len(parsed) == 0 {
		return &convert.Result{Text: "", CitationType: "page"}
	}

	var (
		buf       []byte
		citations []convert.CitationRange
		charPos   int
	)
	for i, p := range parsed {
		loc := "unknown"
		if p.number > 0 {
			loc = "physical_page_" + strconv.Itoa(p.number)
		}
		citations = append(citations, convert.CitationRange{
			Location:  loc,
			CharStart: charPos,
			CharEnd:   charPos + len(p.text),
		})
		buf = append(buf, p.text...)
		if i < len(parsed)-1 {
			buf = append(buf, '\n')
			charPos += len(p.text) + 1
		} else {
			charPos += len(p.text)
		}
	}
	if len(citations) > 0 {
		citations[len(citations)-1].CharEnd = len(buf)
	}
	return &convert.Result{
		Text:         string(buf),
		CitationType: "page",
		Citations:    citations,
	}
}
