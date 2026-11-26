// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httputil

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	solaceDeprecatedHeader = "X-Solace-Api-Deprecated"
	deprecationHeader      = "Deprecation"
	linkHeader             = "Link"
)

type Deprecation struct {
	NoticeURL   string
	Since       time.Time
	QueryParams []string
	FieldsOnly  bool
	OnHit       func(route string)
}

func WithDeprecation(d Deprecation, next http.HandlerFunc) http.HandlerFunc {
	if d.NoticeURL == "" {
		panic("httputil.WithDeprecation: NoticeURL is required")
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if usesDeprecatedPart(d, r) {
			markDeprecated(w, d)
			if d.OnHit != nil {
				d.OnHit(r.Pattern)
			}
		}
		next(w, r)
	}
}

func usesDeprecatedPart(d Deprecation, r *http.Request) bool {
	if len(d.QueryParams) == 0 {
		return true
	}
	query := r.URL.Query()
	for _, name := range d.QueryParams {
		if query.Has(name) {
			return true
		}
	}
	return false
}

func markDeprecated(w http.ResponseWriter, d Deprecation) {
	h := w.Header()
	h.Set(solaceDeprecatedHeader, d.NoticeURL)
	if d.FieldsOnly {
		return
	}
	if !d.Since.IsZero() {
		h.Set(deprecationHeader, "@"+strconv.FormatInt(d.Since.Unix(), 10))
	}
	link := "<" + d.NoticeURL + `>; rel="deprecation"`
	if existing := h.Get(linkHeader); existing != "" {
		link = strings.Join([]string{existing, link}, ", ")
	}
	h.Set(linkHeader, link)
}
