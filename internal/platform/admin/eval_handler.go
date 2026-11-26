// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/configbuilder"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type evalHandler struct {
	datasets                *eval.DatasetService
	examples                *eval.ExampleService
	exampleArtifacts        *eval.ExampleArtifactService
	evaluators              *eval.EvaluatorService
	experiments             *eval.ExperimentService
	runs                    *eval.RunService
	watchlist               *eval.WatchlistService
	analytics               *platpkg.ResourceReporter
	logger                  *slog.Logger
	exampleArtifactMaxBytes int64

	toolAuthControl toolAuthControlCaller
	toolAuthBroker  broker.Broker
	agentResolver   eval.AgentNameResolver
	peerFilters     toolAuthPeerFilterSource
	mesh            toolAuthMeshLister
	workflowNodes   workflowNodeAgentSource
	namespace       string
	db              *store.DB
}

func rawBodyMap(w http.ResponseWriter, r *http.Request, v any) (map[string]json.RawMessage, bool) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxJSONBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("request body exceeds maximum size of %d MB", maxJSONBodyBytes>>20))
			return nil, false
		}
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return nil, false
	}
	if len(body) == 0 {
		return map[string]json.RawMessage{}, true
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		badRequestFromDecode(w, err)
		return nil, false
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		writeError(w, http.StatusBadRequest, "request body must be a JSON object")
		return nil, false
	}
	return raw, true
}

func evalGenGatewayURL(r *http.Request) (string, error) {
	if origin := strings.TrimRight(r.Header.Get("Origin"), "/"); origin != "" {
		if strings.HasPrefix(origin, "http://") || strings.HasPrefix(origin, "https://") {
			return origin, nil
		}
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	if host == "" {
		return "", fmt.Errorf("cannot derive gateway URL: missing Origin/Host headers")
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto == "http" || proto == "https" {
		scheme = proto
	}
	return fmt.Sprintf("%s://%s", scheme, host), nil
}

var ErrEvalGenGatewayConfig = errors.New("WEBUI_FRONTEND_SERVER_URL is not an http or https scheme and host")

func evalGenConfiguredGatewayURL(r *http.Request) (string, error) {
	if configured := strings.TrimRight(configbuilder.ResolveEnv("WEBUI_FRONTEND_SERVER_URL", ""), "/"); configured != "" {
		u, err := url.Parse(configured)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return "", ErrEvalGenGatewayConfig
		}
		return configured, nil
	}
	if r.Host == "" {
		return "", fmt.Errorf("cannot derive gateway URL: missing Host header")
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host, nil
}
