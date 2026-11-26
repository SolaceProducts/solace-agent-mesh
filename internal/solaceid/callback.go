// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package solaceid

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"
)

const (
	callbackPath            = "/callback"
	callbackShutdownTimeout = 2 * time.Second
)

var (
	signedInPage = callbackPage("You're signed in now.", "Go back to your app.")
	failedPage   = callbackPage("Sign-in did not finish.", "Go back to Solace Agent Mesh and try again.")
)

func listenOnCallbackPort(ports []int) (net.Listener, error) {
	var lastErr error
	for _, port := range ports {
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err == nil {
			return listener, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("%w: %w", ErrNoCallbackPort, lastErr)
}

type callbackResult struct {
	code string
	err  error
}

type callbackServer struct {
	server   *http.Server
	listener net.Listener
	state    string
	claimed  atomic.Bool
	results  chan callbackResult
	outcome  chan error
}

func serveCallback(listener net.Listener, state string) *callbackServer {
	cb := &callbackServer{
		listener: listener,
		state:    state,
		results:  make(chan callbackResult, 1),
		outcome:  make(chan error, 1),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+callbackPath, cb.handle)
	cb.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = cb.server.Serve(listener) }()
	return cb
}

func (cb *callbackServer) handle(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if !sameSecret(query.Get("state"), cb.state) {
		writePage(w, http.StatusBadRequest, failedPage)
		return
	}
	if !cb.claimed.CompareAndSwap(false, true) {
		http.Error(w, "This sign-in has already been handled.", http.StatusConflict)
		return
	}
	code, err := codeFrom(query)
	cb.results <- callbackResult{code: code, err: err}
	if err != nil {
		writePage(w, http.StatusBadRequest, failedPage)
		return
	}
	select {
	case err := <-cb.outcome:
		if err != nil {
			writePage(w, http.StatusBadRequest, failedPage)
			return
		}
		writePage(w, http.StatusOK, signedInPage)
	case <-r.Context().Done():
	}
}

func codeFrom(query url.Values) (string, error) {
	if query.Get("error") != "" {
		return "", fail("denied", ErrDenied)
	}
	code := query.Get("code")
	if code == "" {
		return "", fail("callback", errors.New("callback has no code"))
	}
	return code, nil
}

func (cb *callbackServer) wait(ctx context.Context) (string, error) {
	select {
	case res := <-cb.results:
		return res.code, res.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (cb *callbackServer) finish(err error) {
	cb.outcome <- err
}

func (cb *callbackServer) close() {
	ctx, cancel := context.WithTimeout(context.Background(), callbackShutdownTimeout)
	defer cancel()
	_ = cb.server.Shutdown(ctx)
	_ = cb.listener.Close()
}

func writePage(w http.ResponseWriter, status int, page string) {
	header := w.Header()
	header.Set("Content-Type", "text/html; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, page)
}

func callbackPage(heading, body string) string {
	return `<!doctype html><html lang="en"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1"><title>Solace Agent Mesh</title>` +
		`<style>:root{color-scheme:light dark}` +
		`body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;` +
		`font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif}` +
		`main{text-align:center;padding:0 16px}h1{font-size:1.4rem;font-weight:600;margin:0 0 .5rem}p{margin:0;opacity:.75}</style>` +
		`</head><body><main><h1>` + heading + `</h1><p>` + body + `</p></main></body></html>`
}
