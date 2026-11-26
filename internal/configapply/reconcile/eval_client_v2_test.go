// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const evalV2Base = platform.APIV2Prefix + "/evaluations"

func newEvalV2Client(t *testing.T, mux *http.ServeMux) *Client {
	t.Helper()
	mux.HandleFunc("GET "+APIV2ProbePath, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(entplatform.PaginatedResponse[json.RawMessage]{Data: []json.RawMessage{}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return NewClient(target.Resolved{URL: srv.URL})
}

func TestGetEvalRunResults_ReadsPastMaxListPagesOnV2(t *testing.T) {
	const rows = maxListPages*listPageSize + 1
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+evalV2Base+"/runs/{id}/results", func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("pageNumber"))
		first := (page - 1) * listPageSize
		resp := entplatform.PaginatedResponse[json.RawMessage]{Data: []json.RawMessage{}}
		for i := first; i < min(first+listPageSize, rows); i++ {
			resp.Data = append(resp.Data, json.RawMessage(fmt.Sprintf(`{"id":"rr-%d","type":"runResult","scores":[]}`, i)))
		}
		if first+listPageSize < rows {
			next := page + 1
			resp.Meta.Pagination.NextPage = &next
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	c := newEvalV2Client(t, mux)

	got, err := c.GetEvalRunResults(t.Context(), "run-1")
	if err != nil {
		t.Fatalf("GetEvalRunResults: %v", err)
	}
	if len(got) != rows {
		t.Fatalf("got %d results, want %d", len(got), rows)
	}
}

func TestTriggerExperimentRun_RetriesTheRunGroupReadOnV2(t *testing.T) {
	for name, tc := range map[string]struct {
		failures int32
		wantErr  bool
	}{
		"recovers": {failures: 1},
		"gives up": {failures: uploadAttempts, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			var triggers, groupReads atomic.Int32
			mux := http.NewServeMux()
			mux.HandleFunc("PUT "+evalV2Base+"/experiments/{id}/trigger", func(w http.ResponseWriter, _ *http.Request) {
				triggers.Add(1)
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"id":"op-1","type":"operation","operationType":"run","status":"pending"}}`))
			})
			mux.HandleFunc("GET "+evalV2Base+"/runGroups/{id}", func(w http.ResponseWriter, _ *http.Request) {
				if groupReads.Add(1) <= tc.failures {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				_, _ = w.Write([]byte(`{"data":{"id":"op-1","type":"runGroup","runs":[{"id":"run-1","type":"run","status":"pending","summaryScores":[]}]}}`))
			})
			c := newEvalV2Client(t, mux)

			trig, err := c.TriggerExperimentRun(t.Context(), "exp-1", "")
			if triggers.Load() != 1 {
				t.Errorf("triggers = %d, want 1", triggers.Load())
			}
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "may still be running") {
					t.Fatalf("err = %v, want it to say the runs may still be running", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("TriggerExperimentRun: %v", err)
			}
			if len(trig.Runs) != 1 || trig.Runs[0].ID != "run-1" {
				t.Errorf("runs = %+v, want run-1 from the run group", trig.Runs)
			}
		})
	}
}
