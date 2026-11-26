// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestA2AOutbound_BodyReadFailureDoesNotExposeAddresses(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	target := newInternalTarget(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100000")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"name":`)
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	})
	endpoints := newSSRFEndpoints(t, nil, nil)
	logs := captureDefaultLogs(t)

	res := endpoints.postCardURL(t, target.srv.URL+"/card.json")

	want := "failed to read agent card response: the connection was closed by the server"
	if res.status != http.StatusUnprocessableEntity || res.message(t) != want {
		t.Fatalf("got %d %q, want 422 %q", res.status, res.message(t), want)
	}
	if strings.Contains(res.message(t), "127.0.0.1") {
		t.Errorf("message names an address: %q", res.message(t))
	}
	records := logRecords(t, logs)
	if len(records) != 1 || records[0]["error"] == nil {
		t.Fatalf("records = %v, want one line carrying the cause", records)
	}
}
