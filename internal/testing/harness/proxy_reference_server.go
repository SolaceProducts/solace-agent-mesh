// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	a2av1 "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2acompat/a2av0"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/cardsign"
)

const (
	referenceJSONRPCPath = "/a2a/jsonrpc"
	referenceRESTPath    = "/a2a/rest"
	referenceV03Path     = "/a2a/v03"
	referenceJWKSPath    = "/.well-known/jwks.json"
)

const (
	referenceV1JSONRPC  = "1.0/JSONRPC"
	referenceV1HTTPJSON = "1.0/HTTP+JSON"
	referenceV03JSONRPC = "0.3/JSONRPC"
)

type ReferenceA2AConfig struct {
	Interfaces    []string    `yaml:"interfaces"`
	Streaming     bool        `yaml:"streaming"`
	Events        []yaml.Node `yaml:"events"`
	RejectVersion []string    `yaml:"reject_version"`
	Signed        bool        `yaml:"signed"`
}

type ReferenceA2AServer struct {
	Server *httptest.Server

	cfg     ReferenceA2AConfig
	events  []json.RawMessage
	signKey *cardsign.Key

	mu       sync.Mutex
	requests []RecordedRequest
}

func NewReferenceA2AServer(cfg ReferenceA2AConfig) (*ReferenceA2AServer, error) {
	s := &ReferenceA2AServer{cfg: cfg}
	for i, node := range cfg.Events {
		var generic any
		if err := node.Decode(&generic); err != nil {
			return nil, fmt.Errorf("events[%d]: %w", i, err)
		}
		raw, err := json.Marshal(generic)
		if err != nil {
			return nil, fmt.Errorf("events[%d]: %w", i, err)
		}
		s.events = append(s.events, raw)
	}

	handler := a2asrv.NewHandler(a2asrv.AgentExecutorFunc(s.execute))
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+a2asrv.WellKnownAgentCardPath, s.serveCard)
	if cfg.Signed {
		key, err := cardsign.NewKey("reference-1")
		if err != nil {
			return nil, err
		}
		s.signKey = key
		mux.HandleFunc("GET "+referenceJWKSPath, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(cardsign.JWKS(key))
		})
	}
	mux.Handle(referenceJSONRPCPath, s.versionGate(referenceV1JSONRPC, a2asrv.NewJSONRPCHandler(handler)))
	mux.Handle(referenceRESTPath+"/", http.StripPrefix(referenceRESTPath, a2asrv.NewRESTHandler(handler)))
	mux.Handle(referenceV03Path, s.versionGate(referenceV03JSONRPC, a2av0.NewJSONRPCHandler(handler)))
	s.Server = httptest.NewServer(s.record(mux))
	return s, nil
}

func (s *ReferenceA2AServer) RecordedRequests() []RecordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]RecordedRequest(nil), s.requests...)
}

func (s *ReferenceA2AServer) Close() {
	s.Server.Close()
}

func (s *ReferenceA2AServer) record(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		s.mu.Lock()
		s.requests = append(s.requests, RecordedRequest{Method: r.Method, Path: r.URL.Path, Headers: r.Header.Clone(), Body: body})
		s.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func (s *ReferenceA2AServer) versionGate(iface string, next http.Handler) http.Handler {
	for _, rejected := range s.cfg.RejectVersion {
		if rejected != iface {
			continue
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var call struct {
				ID any `json:"id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&call)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      call.ID,
				"error":   map[string]any{"code": -32009, "message": "version not supported"},
			})
		})
	}
	return next
}

func (s *ReferenceA2AServer) serveCard(w http.ResponseWriter, _ *http.Request) {
	card := s.card()
	var body any = card
	if len(s.cfg.Interfaces) == 1 && s.cfg.Interfaces[0] == referenceV03JSONRPC {
		body = a2av0.FromV1AgentCard(card)
	}
	if s.signKey != nil {
		signed, err := s.signedCard(body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		body = signed
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func (s *ReferenceA2AServer) JWKSURL() string {
	return s.Server.URL + referenceJWKSPath
}

func (s *ReferenceA2AServer) signedCard(body any) (map[string]any, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var card map[string]any
	if err := json.Unmarshal(raw, &card); err != nil {
		return nil, err
	}
	return cardsign.Sign(card, s.signKey, s.JWKSURL())
}

func (s *ReferenceA2AServer) card() *a2av1.AgentCard {
	card := &a2av1.AgentCard{
		Name:               "ReferenceAgent",
		Description:        "A2A reference agent replaying scripted events",
		Version:            "1.0.0",
		Capabilities:       a2av1.AgentCapabilities{Streaming: s.cfg.Streaming},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Skills:             []a2av1.AgentSkill{{ID: "replay", Name: "replay", Description: "Replays scripted events", Tags: []string{"test"}}},
	}
	for _, iface := range s.cfg.Interfaces {
		version, binding, _ := strings.Cut(iface, "/")
		path := referenceJSONRPCPath
		switch iface {
		case referenceV1HTTPJSON:
			path = referenceRESTPath
		case referenceV03JSONRPC:
			path = referenceV03Path
		}
		card.SupportedInterfaces = append(card.SupportedInterfaces, &a2av1.AgentInterface{
			URL:             s.Server.URL + path,
			ProtocolBinding: a2av1.TransportProtocol(binding),
			ProtocolVersion: a2av1.ProtocolVersion(version),
		})
	}
	return card
}

func (s *ReferenceA2AServer) execute(_ context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2av1.Event, error] {
	return func(yield func(a2av1.Event, error) bool) {
		events, err := s.script(ec)
		if err != nil {
			yield(nil, err)
			return
		}
		for _, ev := range events {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

func (s *ReferenceA2AServer) script(ec *a2asrv.ExecutorContext) ([]a2av1.Event, error) {
	events := make([]a2av1.Event, 0, len(s.events)+1)
	for i, raw := range s.events {
		var sr a2av1.StreamResponse
		if err := json.Unmarshal(raw, &sr); err != nil {
			return nil, fmt.Errorf("scripted event %d: %w", i, err)
		}
		switch e := sr.Event.(type) {
		case *a2av1.Task:
			e.ID, e.ContextID = ec.TaskID, ec.ContextID
		case *a2av1.TaskStatusUpdateEvent:
			e.TaskID, e.ContextID = ec.TaskID, ec.ContextID
		case *a2av1.TaskArtifactUpdateEvent:
			e.TaskID, e.ContextID = ec.TaskID, ec.ContextID
		case *a2av1.Message:
			e.ContextID = ec.ContextID
		}
		events = append(events, sr.Event)
	}
	if len(events) > 0 {
		switch events[0].(type) {
		case *a2av1.Task, *a2av1.Message:
			return events, nil
		}
	}
	if ec.StoredTask == nil {
		events = append([]a2av1.Event{a2av1.NewSubmittedTask(ec, ec.Message)}, events...)
	}
	return events, nil
}
