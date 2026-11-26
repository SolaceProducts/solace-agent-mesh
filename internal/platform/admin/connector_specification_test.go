// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

var specificationConnectorSeq atomic.Int32

func seedOpenAPIConnectorRow(t *testing.T, repo *store.ConnectorRepo, specURL string) string {
	t.Helper()
	rawValues, err := json.Marshal(map[string]any{"base_url": "https://api.example.com", "specification_url": specURL})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	values, subtype := string(rawValues), "openapi"
	conn := &store.Connector{
		Name: fmt.Sprintf("spec-%d", specificationConnectorSeq.Add(1)), Type: "api", Subtype: &subtype,
		Values: &values, CreatedBy: "tester", UpdatedBy: "tester",
	}
	if err := repo.Create(t.Context(), conn); err != nil {
		t.Fatalf("seed connector: %v", err)
	}
	return conn.ID
}

func TestConnectorSpecification_StalledHostIsCutOff(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	release := make(chan struct{})
	target := newInternalTarget(t, func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	t.Cleanup(func() { close(release) })
	reader := NewConnectorSpecificationReader()
	reader.timeout = 200 * time.Millisecond

	start := time.Now()
	_, err := reader.Read(t.Context(), target.srv.URL+"/spec")

	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("request took %s, the timeout did not apply", elapsed)
	}
	failure, ok := errors.AsType[*ExternalServiceError](err)
	if !ok || !strings.Contains(failure.Message, "the request timed out") {
		t.Fatalf("err = %T %v, want an ExternalServiceError naming the timeout", err, err)
	}
}
