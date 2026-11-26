// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package webhook

import (
	"net/http"
	"strings"
)

type router struct {
	mux        *http.ServeMux
	pathParams map[string][]string
}

type dispatchFunc func(route *RouteConfig, w http.ResponseWriter, r *http.Request)

func buildRouter(routes []*RouteConfig, dispatch dispatchFunc) *router {
	rt := &router{
		mux:        http.NewServeMux(),
		pathParams: make(map[string][]string, len(routes)),
	}

	for _, route := range routes {
		pattern := servePattern(route.Match)
		rt.pathParams[route.Name] = pathParamNames(route.Match.Path)

		r := route
		rt.mux.HandleFunc(pattern, func(w http.ResponseWriter, req *http.Request) {
			dispatch(r, w, req)
		})
	}

	return rt
}

func servePattern(m RouteMatch) string {
	if m.Method == "" {
		return m.Path
	}
	return strings.ToUpper(m.Method) + " " + m.Path
}

func pathParamNames(path string) []string {
	var names []string
	for seg := range strings.SplitSeq(path, "/") {
		if len(seg) < 2 || seg[0] != '{' || seg[len(seg)-1] != '}' {
			continue
		}
		name := strings.TrimSuffix(seg[1:len(seg)-1], "...")
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

func (rt *router) extractPathParams(routeName string, r *http.Request) map[string]string {
	names := rt.pathParams[routeName]
	if len(names) == 0 {
		return nil
	}
	out := make(map[string]string, len(names))
	for _, n := range names {
		out[n] = r.PathValue(n)
	}
	return out
}

func (rt *router) Handler() http.Handler { return rt.mux }
