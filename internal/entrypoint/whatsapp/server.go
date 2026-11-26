// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/hmacutil"
)

const maxWebhookBody = 4 << 20

type Server struct {
	mux *http.ServeMux
	cfg *WhatsAppConfig

	baseCtx        context.Context
	messageHandler func(ctx context.Context, value *webhookChangeValue, msg *inboundMessage) error
	statusHandler  func(value *webhookChangeValue, status *messageStatus)
	logger         *slog.Logger

	mu     sync.Mutex
	closed bool

	wg sync.WaitGroup
}

func NewServer(
	baseCtx context.Context,
	cfg *WhatsAppConfig,
	messageHandler func(ctx context.Context, value *webhookChangeValue, msg *inboundMessage) error,
	statusHandler func(value *webhookChangeValue, status *messageStatus),
	logger *slog.Logger,
) *Server {
	return &Server{
		cfg:            cfg,
		baseCtx:        baseCtx,
		messageHandler: messageHandler,
		statusHandler:  statusHandler,
		logger:         logger,
	}
}

func (s *Server) Handler() http.Handler {
	if s.mux == nil {
		s.buildMux()
	}
	return s.mux
}

func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

func (s *Server) Wait() {
	s.wg.Wait()
}

func (s *Server) enter() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.wg.Add(1)
	return true
}

func (s *Server) leave() {
	s.wg.Done()
}

func (s *Server) buildMux() {
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("GET /webhook", s.handleVerify)
	s.mux.HandleFunc("POST /webhook", s.handleEvent)
	s.mux.HandleFunc("GET /health", s.handleHealth)
}

func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	mode := q.Get("hub.mode")
	token := q.Get("hub.verify_token")
	challenge := q.Get("hub.challenge")

	tokenOK := subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.VerifyToken)) == 1
	if mode != "subscribe" || !tokenOK {
		s.logger.Warn("webhook verification failed", "mode", mode, "clientIP", r.RemoteAddr)
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(challenge))
	s.logger.Info("webhook verified")
}

func (s *Server) handleEvent(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxWebhookBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			s.logger.Warn("webhook body exceeds cap",
				"limit_bytes", maxWebhookBody, "remote", r.RemoteAddr)
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		s.logger.Warn("webhook body read failed", "error", err, "remote", r.RemoteAddr)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if !s.validSignature(r.Header.Get("X-Hub-Signature-256"), body) {
		s.logger.Warn("webhook signature validation failed", "clientIP", r.RemoteAddr)
		w.WriteHeader(http.StatusForbidden)
		return
	}

	var env webhookEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		s.logger.Error("invalid webhook JSON after valid signature", "error", err, "body_bytes", len(body))
		w.WriteHeader(http.StatusOK)
		return
	}

	if !s.enter() {
		s.logger.Warn("webhook delivery refused during shutdown", "entries", len(env.Entry))
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	defer s.leave()

	ctx, cancel := context.WithTimeout(s.baseCtx, spoolBudget)
	defer cancel()

	if err := s.dispatchEnvelope(ctx, &env); err != nil {
		s.logger.Error("failed to spool inbound webhook batch", "error", err, "entries", len(env.Entry))
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) dispatchEnvelope(ctx context.Context, env *webhookEnvelope) error {
	for i := range env.Entry {
		for j := range env.Entry[i].Changes {
			value := &env.Entry[i].Changes[j].Value
			for k := range value.Messages {
				if s.messageHandler == nil {
					continue
				}
				if err := s.messageHandler(ctx, value, &value.Messages[k]); err != nil {
					return err
				}
			}
			for k := range value.Statuses {
				if s.statusHandler != nil {
					s.statusHandler(value, &value.Statuses[k])
				}
			}
		}
	}
	return nil
}

func (s *Server) validSignature(header string, body []byte) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	want, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return false
	}
	return hmacutil.Valid([]byte(s.cfg.AppSecret), body, want)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
