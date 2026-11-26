// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package activation

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const ErrorTypeActivationRequired = "activation_required"

const (
	activationPath      = "/api/v1/activation"
	activationTermsPath = activationPath + "/terms"
)

var pathsOpenBeforeActivation = map[string]bool{
	"/api/v1/config":                   true,
	"/api/v1/config/features":          true,
	"/api/v1/config/capabilities":      true,
	"/api/v1/csrf-token":               true,
	"/api/v2/chat/config":              true,
	"/api/v2/chat/config/login":        true,
	"/api/v1/version":                  true,
	"/api/v2/platform/modelStatus":     true,
	"/api/v1/productAnalytics/ingest":  true,
	"/api/v1/productAnalytics/consent": true,
	activationPath:                     true,
	activationTermsPath:                true,
}

func (s *Service) Gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Active() || isCORSPreflight(r) || !gated(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		s.refuseBeforeActivation(w, r)
	})
}

func isCORSPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
}

func gated(path string) bool {
	if !strings.HasPrefix(path, "/api/") && path != "/gw" && !strings.HasPrefix(path, "/gw/") {
		return false
	}
	path = strings.TrimSuffix(path, "/")
	return !pathsOpenBeforeActivation[path] && !strings.HasPrefix(path, activationPath+":")
}

func (s *Service) refuseBeforeActivation(w http.ResponseWriter, r *http.Request) {
	errorID := idgen.New()
	s.logger.Debug("request refused before activation", "errorId", errorID, "method", r.Method, "path", r.URL.Path)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"message":   "Sign in with Solace ID and accept the license agreement to use Solace Agent Mesh.",
		"errorId":   errorID,
		"errorType": ErrorTypeActivationRequired,
	})
}
