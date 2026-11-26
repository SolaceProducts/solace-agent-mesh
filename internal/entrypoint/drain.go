// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"net/http"
)

type drainSignalKey struct{}

func withDrainSignal(next http.Handler, draining <-chan struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), drainSignalKey{}, draining)))
	})
}

func StreamContext(r *http.Request) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(r.Context())
	draining, ok := r.Context().Value(drainSignalKey{}).(<-chan struct{})
	if !ok {
		return ctx, cancel
	}
	go func() {
		select {
		case <-draining:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}
