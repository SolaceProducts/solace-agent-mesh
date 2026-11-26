// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apicontract

import (
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type Route struct {
	Method        string
	Path          string
	Scope         string
	AnyOfScopes   []string
	Gate          Gate
	AuditResource string
	Published     bool
	AllowAPIKey   bool
	Stability     Stability
	MaxBodyBytes  int64
	Doc           Doc
	Deprecation   *httputil.Deprecation
	Handler       Handler
}

type Gate string

type Stability string

const (
	StabilityDraft  Stability = "draft"
	StabilityStable Stability = "stable"
)

type Doc struct {
	ID        string
	Summary   string
	Text      string
	Tag       Tag
	Errors    []int
	FileTypes []string
}

type Tag struct {
	Name  string
	Group string
	Text  string
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.serve(w, r) }

func (h Handler) WriteSample(w http.ResponseWriter, r *http.Request) { h.writeSample(w, r) }

func (rt Route) Scopes() []string {
	switch {
	case rt.Gate != "":
		return nil
	case len(rt.AnyOfScopes) > 0:
		return rt.AnyOfScopes
	default:
		return []string{rt.Scope}
	}
}

type Guard func(rt Route, next http.HandlerFunc) http.HandlerFunc

func Mount(mux *http.ServeMux, prefix string, routes []Route, guard Guard, allowAPIKey func(pattern string)) {
	for _, rt := range routes {
		pattern := rt.Method + " " + prefix + rt.Path
		mux.HandleFunc(pattern, guard(rt, Deprecated(rt.Deprecation, rt.CapBody(rt.Handler.serve))))
		if rt.AllowAPIKey && rt.Published && allowAPIKey != nil {
			allowAPIKey(pattern)
		}
	}
}

const (
	deprecatedRequestsMetric = "api.deprecated.requests"
	deprecationKindRoute     = "route"
	deprecationKindFields    = "fields"
)

func deprecationKind(d *httputil.Deprecation) string {
	if d.FieldsOnly {
		return deprecationKindFields
	}
	return deprecationKindRoute
}

func Deprecated(d *httputil.Deprecation, next http.HandlerFunc) http.HandlerFunc {
	if d == nil {
		return next
	}
	kind := deprecationKind(d)
	counted := *d
	counted.OnHit = func(route string) {
		if d.OnHit != nil {
			d.OnHit(route)
		}
		runtime.MetricProvider().Counter(deprecatedRequestsMetric, 1, "route.template", route, "deprecation.kind", kind)
	}
	return httputil.WithDeprecation(counted, next)
}

func (rt Route) CapBody(next http.HandlerFunc) http.HandlerFunc {
	if rt.MaxBodyBytes <= 0 {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, rt.MaxBodyBytes)
		next(w, r)
	}
}
