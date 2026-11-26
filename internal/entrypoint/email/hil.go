// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type pendingHIL struct {
	surface   *a2ui.Surface
	taskID    string
	agentName string
	userID    string
	expiry    time.Time
}

const hilAlreadyAnsweredTitle = "Already answered"

type hilServer struct {
	core    *entrypoint.GatewayCore
	secret  []byte
	baseURL string
	gwID    string
	logger  *slog.Logger
	items   taskItems

	mu      sync.Mutex
	pending map[string]*pendingHIL
}

func newHILServer(core *entrypoint.GatewayCore, secret []byte, baseURL, gwID string, logger *slog.Logger) *hilServer {
	return &hilServer{
		core:    core,
		secret:  secret,
		baseURL: baseURL,
		gwID:    gwID,
		logger:  logger,
		pending: map[string]*pendingHIL{},
	}
}

func (s *hilServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /userInput/{token}", s.handleGet)
	mux.HandleFunc("POST /userInput/{token}", s.handlePost)
	return mux
}

func (s *hilServer) register(requestID string, p *pendingHIL) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, e := range s.pending {
		if now.After(e.expiry) {
			delete(s.pending, id)
		}
	}
	s.pending[requestID] = p
}

func (s *hilServer) claim(requestID string) (*pendingHIL, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[requestID]
	if ok {
		delete(s.pending, requestID)
	}
	return p, ok
}

func (s *hilServer) peek(requestID string) (*pendingHIL, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[requestID]
	return p, ok
}

func (s *hilServer) save(ctx context.Context, requestID string, p *pendingHIL, surface json.RawMessage) error {
	if !s.items.enabled() {
		s.register(requestID, p)
		return nil
	}
	value, err := json.Marshal(hilPromptItem{Surface: surface, AgentName: p.agentName, UserID: p.userID, ExpiresAt: p.expiry.Unix()})
	if err != nil {
		return fmt.Errorf("encode HIL prompt: %w", err)
	}
	ctx, cancel := s.items.bounded(ctx)
	defer cancel()
	return s.items.repo.Put(ctx, p.taskID, itemHILPromptPrefix+requestID, string(value))
}

func (s *hilServer) lookup(ctx context.Context, taskID, requestID string) (*pendingHIL, bool, error) {
	if !s.items.enabled() {
		p, ok := s.peek(requestID)
		return p, ok, nil
	}
	raw, ok, err := s.items.get(ctx, taskID, itemHILPromptPrefix+requestID)
	if err != nil || !ok {
		return nil, false, err
	}
	var item hilPromptItem
	if decodeErr := json.Unmarshal([]byte(raw), &item); decodeErr != nil {
		return nil, false, fmt.Errorf("decode HIL prompt: %w", decodeErr)
	}
	surface, err := a2ui.Parse(item.Surface)
	if err != nil {
		return nil, false, fmt.Errorf("parse HIL surface: %w", err)
	}
	return &pendingHIL{surface: surface, taskID: taskID, agentName: item.AgentName, userID: item.UserID, expiry: time.Unix(item.ExpiresAt, 0)}, true, nil
}

func (s *hilServer) answered(ctx context.Context, taskID, requestID string) (bool, error) {
	if !s.items.enabled() {
		return false, nil
	}
	_, ok, err := s.items.get(ctx, taskID, itemHILAnsweredPrefix+requestID)
	return ok, err
}

func (s *hilServer) linkFor(token string) string {
	return fmt.Sprintf("%s/gw/%s/userInput/%s", s.baseURL, s.gwID, token)
}

func (s *hilServer) handleGet(w http.ResponseWriter, r *http.Request) {
	tok, err := verifyHILToken(r.PathValue("token"), s.secret)
	if err == errTokenExpired {
		s.page(w, http.StatusGone, "Link expired", "This response link has expired. Ask the agent to prompt you again.")
		return
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p, ok, err := s.lookup(r.Context(), tok.TaskID, tok.RequestID)
	if err == nil && ok {
		var done bool
		done, err = s.answered(r.Context(), tok.TaskID, tok.RequestID)
		ok = !done
	}
	if err != nil {
		s.logger.Warn("HIL prompt could not be read", "error", err, "taskID", tok.TaskID, "requestID", tok.RequestID)
		s.page(w, http.StatusServiceUnavailable, "Try again", "The form could not be loaded. Please try again shortly.")
		return
	}
	if !ok {
		s.page(w, http.StatusGone, hilAlreadyAnsweredTitle, "This request has already been answered or is no longer waiting.")
		return
	}
	if err := renderHILForm(w, p.surface, s.linkFor(r.PathValue("token"))); err != nil {
		s.logger.Error("failed to render HIL form", "error", err, "requestID", tok.RequestID)
		s.page(w, http.StatusInternalServerError, "Error", "Could not render the form.")
	}
}

func (s *hilServer) handlePost(w http.ResponseWriter, r *http.Request) {
	tok, err := verifyHILToken(r.PathValue("token"), s.secret)
	if err == errTokenExpired {
		s.page(w, http.StatusGone, "Link expired", "This response link has expired.")
		return
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !s.core.IsTaskRunning(tok.TaskID) {
		s.page(w, http.StatusGone, "No longer waiting", "This request is no longer waiting for a response.")
		return
	}

	var p *pendingHIL
	var ok bool
	if s.items.enabled() {
		var err error
		if p, ok, err = s.lookup(r.Context(), tok.TaskID, tok.RequestID); err == nil && ok {
			var done bool
			done, err = s.answered(r.Context(), tok.TaskID, tok.RequestID)
			if done {
				s.page(w, http.StatusGone, hilAlreadyAnsweredTitle, "This request has already been answered.")
				return
			}
		}
		if err != nil {
			s.logger.Warn("HIL prompt could not be read", "error", err, "taskID", tok.TaskID, "requestID", tok.RequestID)
			s.page(w, http.StatusServiceUnavailable, "Try again", "Your response could not be processed. Please try again shortly.")
			return
		}
	} else {
		p, ok = s.claim(tok.RequestID)
	}
	if !ok {
		s.page(w, http.StatusGone, hilAlreadyAnsweredTitle, "This request has already been answered.")
		return
	}

	status, data, ok := parseHILForm(r, p.surface)
	if !ok {
		s.page(w, http.StatusBadRequest, "Invalid submission", "The form could not be processed.")
		return
	}

	resp := a2a.UserInputResponseData{
		Type:      "user_input_response",
		RequestID: tok.RequestID,
		Status:    status,
		Data:      data,
	}
	if err := s.core.SubmitUserInputResponse(r.Context(), tok.AgentName, tok.TaskID, resp, auth.ForEmailHILResponder(tok.UserID)); err != nil {
		s.logger.Error("failed to submit email HIL response", "error", err, "taskID", tok.TaskID, "agentName", tok.AgentName)
		s.page(w, http.StatusInternalServerError, "Error", "Could not deliver your response. Please try again later.")
		return
	}
	s.logger.Info("email HIL response submitted", "requestID", tok.RequestID, "taskID", tok.TaskID, "taskStatus", status)
	s.items.put(r.Context(), tok.TaskID, itemHILAnsweredPrefix+tok.RequestID, "")

	msg := "Thanks — your response has been sent to the agent. You can close this page."
	if status == "cancelled" {
		msg = "The request has been cancelled. You can close this page."
	}
	s.page(w, http.StatusOK, "Response received", msg)
}

func (s *hilServer) page(w http.ResponseWriter, code int, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8">`+
		`<meta name="viewport" content="width=device-width, initial-scale=1"><title>%s</title>`+
		`<style>body{font-family:system-ui,sans-serif;max-width:560px;margin:3rem auto;padding:0 1rem}</style>`+
		`</head><body><h2>%s</h2><p>%s</p></body></html>`, title, title, body)
}

func (a *EmailAdapter) Capabilities() map[string]bool {
	return map[string]bool{tool.CapInteractiveUserInput: a.hil != nil}
}

var (
	_ entrypoint.CapabilityProvider  = (*EmailAdapter)(nil)
	_ entrypoint.DeferredHTTPAdapter = (*EmailAdapter)(nil)
)

func (a *EmailAdapter) SetOnHandlerReady(cb func(http.Handler)) {
	a.onHandlerReady = cb
}

func (a *EmailAdapter) hilHandler() http.Handler {
	if a.hil != nil {
		return a.hil.Handler()
	}
	return http.NotFoundHandler()
}

func (a *EmailAdapter) sendHILEmail(ctx context.Context, taskID string, meta *EmailTaskMeta, req *a2a.UserInputRequestData) {
	surface, err := a2ui.Parse(req.Surface)
	if err != nil {
		a.logger.Error("failed to parse A2UI surface", "error", err, "requestID", req.RequestID)
		return
	}

	expiry := time.Now().Add(a.cfg.HILTokenTTL)
	if req.ExpiresAt != "" {
		if t, perr := time.Parse(time.RFC3339, req.ExpiresAt); perr == nil {
			expiry = t
		}
	}

	agentName := ""
	if info := a.core.Tasks().Get(taskID); info != nil {
		agentName = info.AgentName
	}

	if saveErr := a.hil.save(ctx, req.RequestID, &pendingHIL{
		surface:   surface,
		taskID:    taskID,
		agentName: agentName,
		userID:    meta.UserID,
		expiry:    expiry,
	}, req.Surface); saveErr != nil {
		a.logger.Error("failed to record HIL prompt; the link is not sent", "error", saveErr, "taskID", taskID, "requestID", req.RequestID)
		return
	}

	token, err := signHILToken(hilToken{
		RequestID: req.RequestID,
		TaskID:    taskID,
		AgentName: agentName,
		UserID:    meta.UserID,
		ExpiresAt: expiry.Unix(),
	}, a.hil.secret)
	if err != nil {
		a.logger.Error("failed to sign HIL token", "error", err, "requestID", req.RequestID)
		return
	}

	body := hilEmailBody(surface, a.hil.linkFor(token))

	if err := a.replier.Send(ctx, ReplyRequest{
		To:                 meta.ReplyTo.Address,
		Subject:            meta.Subject,
		BodyText:           body,
		OriginalMessageID:  meta.OriginalMessageID,
		OriginalReferences: meta.OriginalReferences,
	}); err != nil {
		a.logger.Error("failed to send HIL email", "error", err, "taskID", taskID)
		return
	}
	a.logger.LogAttrs(ctx, slog.LevelInfo, "HIL magic-link sent",
		slog.String("eventType", "email.hil_link_sent"),
		slog.String("taskID", taskID),
		slog.String("requestID", req.RequestID),
	)
}
