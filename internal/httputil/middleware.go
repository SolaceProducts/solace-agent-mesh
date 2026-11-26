// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httputil //nolint:revive // internal package, no conflict risk

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/rs/cors"
)

type contextKey int

const (
	requestIDKey contextKey = iota
	loggerKey
)

func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func LoggerFromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = idgen.New()
		}
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		ctx = context.WithValue(ctx, loggerKey, slog.Default().With("requestID", id))
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

var baselineSecurityHeaders = map[string]string{
	"X-Content-Type-Options": "nosniff",
	"Referrer-Policy":        "strict-origin-when-cross-origin",
	"Permissions-Policy":     "camera=(), geolocation=(), payment=(), usb=(), microphone=(self)",
}

func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		for name, value := range baselineSecurityHeaders {
			h.Set(name, value)
		}
		next.ServeHTTP(w, r)
	})
}

const hstsHeaderValue = "max-age=63072000; includeSubDomains; preload"

func HSTSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", hstsHeaderValue)
		}
		next.ServeHTTP(w, r)
	})
}

func NoStoreAPIMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h := w.Header()
			h.Set("Cache-Control", "no-store")
			h.Set("Pragma", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

func CompileCORSOriginRegex(pattern string, logger *slog.Logger) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, nil
	}
	hasStart := strings.HasPrefix(pattern, "^")
	hasEnd := strings.HasSuffix(pattern, "$")
	if logger != nil && (!hasStart || !hasEnd) {
		logger.Warn(
			"cors_allowed_origin_regex is not fully anchored; auto-anchoring with ^(?:...)$",
			"pattern", pattern,
			"missingStartAnchor", !hasStart,
			"missingEndAnchor", !hasEnd,
		)
	}
	wrapped := "^(?:" + pattern + ")$"
	re, err := regexp.Compile(wrapped)
	if err != nil {
		return nil, err
	}
	return re, nil
}

func CORSMiddleware(allowedOrigins []string, originRegex *regexp.Regexp) func(http.Handler) http.Handler {
	return CORSMiddlewareWithExposedHeaders(allowedOrigins, originRegex, nil)
}

func CORSMiddlewareWithExposedHeaders(allowedOrigins []string, originRegex *regexp.Regexp, exposedHeaders []string) func(http.Handler) http.Handler {
	allowAll := len(allowedOrigins) == 0
	originSet := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if o == "*" {
			allowAll = true
		}
		originSet[o] = true
	}

	opts := cors.Options{
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"*"},
		ExposedHeaders:   exposedHeaders,
		AllowCredentials: true,
	}

	if allowAll {
		opts.AllowOriginFunc = func(origin string) bool { return true }
	} else {
		opts.AllowOriginFunc = func(origin string) bool {
			return originSet[origin] || (originRegex != nil && originRegex.MatchString(origin))
		}
	}

	c := cors.New(opts)
	return func(next http.Handler) http.Handler {
		return c.Handler(next)
	}
}

func Chain(middlewares ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(final http.Handler) http.Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			final = middlewares[i](final)
		}
		return final
	}
}

func IsStateChangingMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func errorJSON(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(map[string]any{
		"detail":            msg,
		"message":           msg,
		"validationDetails": nil,
	})
	_, _ = w.Write(body)
}

func MaxBytesMiddleware(limit int64, onReject ...func(r *http.Request, status int)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > limit {
				errorJSON(w, http.StatusRequestEntityTooLarge, "request body exceeds maximum size")
				for _, fn := range onReject {
					if fn != nil {
						fn(r, http.StatusRequestEntityTooLarge)
					}
				}
				return
			}
			http.MaxBytesHandler(next, limit).ServeHTTP(w, r)
		})
	}
}

func RequireJSONMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if IsStateChangingMethod(r.Method) && requestHasBody(r) && !isJSONContentType(r.Header.Get("Content-Type")) {
			errorJSON(w, http.StatusUnsupportedMediaType, "unsupported media type: Content-Type must be application/json")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestHasBody(r *http.Request) bool {
	if r.ContentLength > 0 {
		return true
	}
	if len(r.TransferEncoding) > 0 {
		return true
	}
	return r.ContentLength == -1
}

func isJSONContentType(ct string) bool {
	if ct == "" {
		return false
	}
	mediaType, _, _ := strings.Cut(ct, ";")
	mediaType = strings.TrimSpace(strings.ToLower(mediaType))
	return mediaType == "application/json"
}
