// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

const gatedPetSpec = `{"openapi":"3.0.0","info":{"title":"t","version":"1"},"servers":[{"url":"https://api.example.com"}],"paths":{"/pets":{"get":{"operationId":"listPets","responses":{"200":{"description":"ok"}}}}}}`

func gatedURLConnector(url, description string) BuildComponentInput {
	return connectorWith("petstore", func(b map[string]any) {
		b["type"], b["subtype"] = "api", "openapi"
		b["description"] = description
		b["values"] = apiConnectorValues(map[string]any{
			"specification_url": url,
			hilSetting:          map[string]any{"tools": map[string]any{"list_pets": map[string]any{"require_approval": true}}},
		})
	})
}

func TestApply_FetchesAGatedConnectorsSpecURLOncePerApply(t *testing.T) {
	t.Setenv("SAM_PLATFORM_ALLOW_PRIVATE_MCP", "true")
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, gatedPetSpec)
	}))
	t.Cleanup(srv.Close)
	svc := newTestService(t)
	specURL := srv.URL + "/openapi.json"

	for _, step := range []struct {
		name        string
		description string
	}{{"create", "A pet store connector."}, {"update", "A pet store connector, edited."}} {
		hits.Store(0)
		resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{gatedURLConnector(specURL, step.description)}}, testActor)
		if err != nil || resp.Summary.Failed != 0 {
			t.Fatalf("%s: %v %+v %+v", step.name, err, resp, resp.Operations[0].Error)
		}
		if got := hits.Load(); got != 1 {
			t.Errorf("%s: the apply fetched the specification_url %d times, want 1", step.name, got)
		}
	}
}
