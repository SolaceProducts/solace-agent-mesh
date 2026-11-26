// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"slices"
	"testing"
)

func createDataset(api *entrypointAPI, name string) string {
	api.t.Helper()
	dataset := newDataset(api, name)
	newExample(api, dataset.ID, map[string]any{"prompt": "What is 2 + 2?"})
	return dataset.ID
}

func builtinEvaluatorIDs(api *entrypointAPI) []string {
	api.t.Helper()
	builtins := builtinEvaluators(api)
	if len(builtins) < 2 {
		api.t.Fatalf("want at least two built-in evaluators, got %+v", builtins)
	}
	ids := make([]string, len(builtins))
	for i, e := range builtins {
		ids[i] = e.ID
	}
	return ids
}

func TestEvaluations_APIKeyRoutes(t *testing.T) {
	got := apiKeyRoutesTagged(t, experimentsTag, runsTag, runResultsTag, toolAuthTag, watchlistTag)
	want := []string{
		"DELETE /evaluations/experiments/{id}", "GET /evaluations/experiments",
		"GET /evaluations/experiments/{experimentId}/operations/{id}", "GET /evaluations/experiments/{experimentId}/runs", "GET /evaluations/experiments/{id}",
		"GET /evaluations/runGroups/{id}",
		"GET /evaluations/runResults/{runResultId}/executionData", "GET /evaluations/runResults/{runResultId}/taskEvents",
		"GET /evaluations/runs/{id}", "GET /evaluations/runs/{runId}/results",
		"PATCH /evaluations/experiments/{id}", "POST /evaluations/experiments",
		"PUT /evaluations/experiments/{experimentId}/trigger", "PUT /evaluations/runs/{id}/cancel",
	}
	if !slices.Equal(got, want) {
		t.Errorf("API key routes = %v, want %v", got, want)
	}
}
