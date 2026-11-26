// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sync"
)

type TeamsServer struct {
	mux             *http.ServeMux
	auth            *BotAuthenticator
	activityHandler func(ctx context.Context, activity *Activity) *InvokeResponse
	receive         func(ctx context.Context, activity *Activity, body []byte) (func(), error)
	cfg             *TeamsConfig
	logger          *slog.Logger

	mu     sync.Mutex
	closed bool

	wg sync.WaitGroup
}

func NewTeamsServer(cfg *TeamsConfig, auth *BotAuthenticator, handler func(ctx context.Context, activity *Activity) *InvokeResponse, logger *slog.Logger) *TeamsServer {
	return &TeamsServer{
		auth:            auth,
		activityHandler: handler,
		cfg:             cfg,
		logger:          logger,
	}
}

func (s *TeamsServer) Handler() http.Handler {
	if s.mux == nil {
		s.buildMux()
	}
	return s.mux
}

func (s *TeamsServer) buildMux() {
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("POST /api/messages", s.handleMessages)
	s.mux.HandleFunc("GET /health", s.handleHealth)
}

func (s *TeamsServer) handleMessages(w http.ResponseWriter, r *http.Request) {
	claims, err := s.auth.ValidateAuth(r.Context(), r.Header.Get("Authorization"))
	if err != nil {
		s.logger.Warn("auth validation failed", "error", err, "remote", r.RemoteAddr)
		writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "failed to read body")
		return
	}

	var activity Activity
	if err := json.Unmarshal(body, &activity); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid activity JSON")
		return
	}

	if !claims.ServiceURLMatches(activity.ServiceURL) {
		s.logger.Warn("activity serviceUrl does not match the token's serviceurl claim", "remote", r.RemoteAddr)
		writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	s.logger.Debug("received activity",
		"type", activity.Type,
		"from", activity.From.ID,
		"conversation", activity.Conversation.ID,
	)

	if activity.Type == ActivityTypeInvoke {
		if s.isClosed() {
			s.logger.Warn("invoke refused during shutdown", "name", activity.Name)
			writeJSONError(w, http.StatusServiceUnavailable, shuttingDownRefusal)
			return
		}
		resp := s.activityHandler(r.Context(), &activity)
		if resp == nil {
			resp = &InvokeResponse{Status: http.StatusOK}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.Status)
		if resp.EmptyBody {
			return
		}
		if resp.Body != nil {
			json.NewEncoder(w).Encode(resp.Body) //nolint:errcheck
		} else {
			json.NewEncoder(w).Encode(InvokeResponse{Status: resp.Status}) //nolint:errcheck
		}
		return
	}

	if s.isClosed() {
		s.logger.Warn("activity refused during shutdown", "activityType", activity.Type)
		writeJSONError(w, http.StatusServiceUnavailable, shuttingDownRefusal)
		return
	}
	work, refused := s.receiveActivity(r.Context(), &activity, body)
	if refused != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "temporarily unavailable")
		return
	}
	if work == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !s.startDispatch(work) {
		s.logger.Warn("activity refused during shutdown", "activityType", activity.Type)
		writeJSONError(w, http.StatusServiceUnavailable, shuttingDownRefusal)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *TeamsServer) receiveActivity(ctx context.Context, activity *Activity, body []byte) (func(), error) {
	if s.receive == nil {
		return func() { s.activityHandler(context.Background(), activity) }, nil
	}
	return s.receive(ctx, activity, body)
}

func (s *TeamsServer) startDispatch(work func()) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.wg.Go(func() { s.guarded(work) })
	return true
}

func (s *TeamsServer) TrackBackground(fn func()) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.wg.Go(func() { s.guarded(fn) })
	return true
}

func (s *TeamsServer) guarded(work func()) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("panic in dispatched teams work", "panic", r,
				"panicType", fmt.Sprintf("%T", r), "stack", string(debug.Stack()))
		}
	}()
	work()
}

func (s *TeamsServer) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *TeamsServer) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

func (s *TeamsServer) Wait() {
	s.wg.Wait()
}

func (s *TeamsServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) //nolint:errcheck
}

const shuttingDownRefusal = "shutting down"

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
}
