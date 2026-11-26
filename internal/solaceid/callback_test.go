// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package solaceid

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallbackHandsOnlyTheFirstMatchingRedirectToTheSignIn(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	cb := serveCallback(listener, "the-state")
	t.Cleanup(cb.close)
	redirect := "http://" + listener.Addr().String() + callbackPath + "?" + url.Values{"state": {"the-state"}, "code": {"first"}}.Encode()

	first := make(chan int, 1)
	go func() {
		resp, getErr := http.Get(redirect)
		if getErr != nil {
			first <- 0
			return
		}
		_ = resp.Body.Close()
		first <- resp.StatusCode
	}()
	code, err := cb.wait(context.Background())
	require.NoError(t, err)

	reload := &http.Client{Timeout: 5 * time.Second}
	resp, err := reload.Get(redirect)
	require.NoError(t, err)
	_ = resp.Body.Close()
	cb.finish(nil)

	assert.Equal(t, "first", code)
	assert.Equal(t, http.StatusConflict, resp.StatusCode, "a reload of the redirect must not reach the sign-in")
	assert.Equal(t, http.StatusOK, <-first)
	assert.Empty(t, cb.results, "the second redirect must not queue a code")
}
