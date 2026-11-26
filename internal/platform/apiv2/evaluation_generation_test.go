// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	generatedDataset  = `{"name":"Generated dataset","description":"d","examples":[{"prompt":"q1","expected_response":"r1"},{"prompt":"q2"}]}`
	generatedExamples = `{"examples":[{"prompt":"q3"},{"prompt":"q4","expected_response":"r4"}]}`
	orchestratorFault = "orchestrator stack trace"
	callerAuth        = "Bearer generation-caller"
)

type fakeOrchestrator struct {
	*httptest.Server
	reply string
	hold  chan struct{}

	mu   sync.Mutex
	auth []string
}

func newFakeOrchestrator(t *testing.T, reply string) *fakeOrchestrator {
	t.Helper()
	o := &fakeOrchestrator{reply: reply}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/message:send", func(w http.ResponseWriter, r *http.Request) {
		o.mu.Lock()
		o.auth = append(o.auth, r.Header.Get("Authorization"))
		o.mu.Unlock()
		if o.reply == "" {
			http.Error(w, orchestratorFault, http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":"x","result":{"id":"task-1"}}`)
	})
	mux.HandleFunc("GET /api/v1/sse/subscribe/{taskId}", func(w http.ResponseWriter, r *http.Request) {
		if o.hold != nil {
			select {
			case <-o.hold:
			case <-r.Context().Done():
				return
			}
		}
		parts, _ := json.Marshal([]map[string]string{{"kind": "text", "text": o.reply}})
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "event: msg\ndata: {\"jsonrpc\":\"2.0\",\"id\":\"x\",\"result\":{\"kind\":\"task\",\"status\":{\"message\":{\"parts\":%s}}}}\n\n", parts)
	})
	o.Server = httptest.NewServer(mux)
	t.Cleanup(o.Close)
	return o
}

func (o *fakeOrchestrator) authHeaders() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.auth...)
}

func generate(api *entrypointAPI, o *fakeOrchestrator, path string, body any) *httptest.ResponseRecorder {
	api.t.Helper()
	req := newJSONRequest(api.t, http.MethodPut, path, body)
	req.Host = strings.TrimPrefix(o.URL, "http://")
	req.Header.Set("Origin", o.URL)
	req.Header.Set("Authorization", callerAuth)
	rec := httptest.NewRecorder()
	api.handler.ServeHTTP(rec, req)
	return rec
}

func expectAccepted(t *testing.T, rec *httptest.ResponseRecorder, pathPrefix string) (Operation, string) {
	t.Helper()
	expectStatus(t, rec, http.StatusAccepted)
	op := decodeData[Operation](t, rec)
	path := strings.TrimPrefix(rec.Header().Get("Location"), platpkg.APIV2Prefix)
	if want := pathPrefix + "/" + op.ID; path != want {
		t.Errorf("Location path = %q, want %q", path, want)
	}
	if op.Status != "inProgress" || op.Error != nil {
		t.Errorf("accepted operation = %+v, want in progress", op)
	}
	return op, path
}

func awaitOperation(api *entrypointAPI, path string) (Operation, *httptest.ResponseRecorder) {
	api.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		rec := api.call(http.MethodGet, path, nil)
		expectStatus(api.t, rec, http.StatusOK)
		op := decodeData[Operation](api.t, rec)
		if op.Status != "inProgress" {
			return op, rec
		}
		if time.Now().After(deadline) {
			api.t.Fatalf("operation %s still in progress", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestDatasetGeneration_SuccessCreatesTheDataset(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	o := newFakeOrchestrator(t, generatedDataset)

	rec := generate(api, o, datasetsPath+"/generate", map[string]any{"targetAgent": "support_agent", "numExamples": 2})
	started, path := expectAccepted(t, rec, datasetOperationsPath)
	expectNull(t, rawData(t, rec), "resourceId")
	if started.OperationType != "generate_dataset" {
		t.Errorf("operationType = %q, want generate_dataset", started.OperationType)
	}

	done, rec := awaitOperation(api, path)
	if done.Status != "succeeded" || done.ResourceID == nil || done.Error != nil {
		t.Fatalf("finished operation = %+v, want succeeded naming its dataset", done)
	}
	expectISOTimes(t, rawData(t, rec), "createdTime", "completedTime")
	dataset := decodeData[Dataset](t, api.call(http.MethodGet, datasetsPath+"/"+*done.ResourceID, nil))
	if dataset.Name != "Generated dataset" || dataset.ExampleCount != 2 {
		t.Errorf("generated dataset = %+v, want the AI's name and two examples", dataset)
	}
	if auth := o.authHeaders(); len(auth) != 1 || auth[0] != callerAuth {
		t.Errorf("orchestrator saw Authorization %v, want the caller's", auth)
	}

	ops := decodeList[Operation](t, api.call(http.MethodGet, datasetsPath+"/"+dataset.ID+"/operations", nil))
	if len(ops.Data) != 1 || ops.Data[0].ID != done.ID {
		t.Errorf("dataset operations = %+v, want the generation that created it", ops.Data)
	}
	expectStatus(t, api.call(http.MethodGet, datasetsPath+"/"+dataset.ID+"/operations/"+done.ID, nil), http.StatusOK)
}

func TestDatasetGeneration_FailureCreatesNothing(t *testing.T) {
	logs := captureLogs(t)
	api := newEntrypointAPI(t, nil)
	o := newFakeOrchestrator(t, "")
	before := decodeList[Dataset](t, api.call(http.MethodGet, datasetsPath, nil)).Meta.Pagination.Count

	_, path := expectAccepted(t, generate(api, o, datasetsPath+"/generate",
		map[string]any{"targetAgent": "support_agent", "numExamples": 2}), datasetOperationsPath)
	done, rec := awaitOperation(api, path)
	if done.Status != "failed" || done.ResourceID != nil || done.Error == nil {
		t.Fatalf("finished operation = %+v, want failed with no dataset", done)
	}
	if done.Error.Message != "The AI could not generate the dataset. Try again." {
		t.Errorf("error message = %q, want SAM's sentence", done.Error.Message)
	}
	if strings.Contains(rec.Body.String(), orchestratorFault) {
		t.Errorf("operation carries the orchestrator's text: %s", rec.Body.String())
	}
	if after := decodeList[Dataset](t, api.call(http.MethodGet, datasetsPath, nil)).Meta.Pagination.Count; after != before {
		t.Errorf("dataset count %d after a failed generation, want %d", after, before)
	}
	if !loggedCause(logs.String(), done.Error.ErrorID) {
		t.Errorf("no log line carries errorId %s with the cause:\n%s", done.Error.ErrorID, logs.String())
	}
}

func loggedCause(logs, errorID string) bool {
	for line := range strings.Lines(logs) {
		var entry struct {
			ErrorID string `json:"errorId"`
			Error   string `json:"error"`
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.ErrorID == errorID && strings.Contains(entry.Error, "500") {
			return true
		}
	}
	return false
}

func TestExampleGeneration_AddsExamplesOneBatchAtATime(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	o := newFakeOrchestrator(t, generatedExamples)
	o.hold = make(chan struct{})
	dataset := newDataset(api, "generation-target")
	examplesPath := datasetsPath + "/" + dataset.ID + "/examples/generate"
	body := map[string]any{"targetAgent": "support_agent", "numExamples": 2, "focus": "refunds"}

	started, path := expectAccepted(t, generate(api, o, examplesPath, body), datasetsPath+"/"+dataset.ID+"/operations")
	if started.OperationType != "generate_examples" || started.ResourceID == nil || *started.ResourceID != dataset.ID {
		t.Errorf("accepted operation = %+v, want generate_examples on the dataset", started)
	}
	rec := generate(api, o, examplesPath, body)
	expectStatus(t, rec, http.StatusConflict)
	decodeError(t, rec)

	close(o.hold)
	if done, _ := awaitOperation(api, path); done.Status != "succeeded" {
		t.Fatalf("finished operation = %+v, want succeeded", done)
	}
	if got := decodeData[Dataset](t, api.call(http.MethodGet, datasetsPath+"/"+dataset.ID, nil)); got.ExampleCount != 2 {
		t.Errorf("exampleCount = %d after generating 2", got.ExampleCount)
	}
	expectStatus(t, generate(api, o, examplesPath, body), http.StatusAccepted)
}

func TestGeneration_Errors(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	o := newFakeOrchestrator(t, generatedExamples)
	fullID := createDataset(api, "generation-full")
	other := newDataset(api, "generation-other")
	examplesPath := func(id string) string { return datasetsPath + "/" + id + "/examples/generate" }

	for _, path := range []string{datasetsPath + "/generate", examplesPath(other.ID)} {
		rec := generate(api, o, path, map[string]any{"numExamples": 0})
		expectStatus(t, rec, http.StatusUnprocessableEntity)
		for _, field := range []string{"targetAgent", "numExamples"} {
			if len(decodeError(t, rec).ValidationDetails[field]) == 0 {
				t.Errorf("PUT %s: validationDetails has nothing for %s", path, field)
			}
		}
		expectDetail(t, generate(api, o, path, `{"targetAgent":"a","numExamples":1,"colour":"red"}`), http.StatusBadRequest, "colour")
	}
	expectDetail(t, generate(api, o, examplesPath(fullID), map[string]any{"targetAgent": "support_agent", "numExamples": 5000}),
		http.StatusUnprocessableEntity, "numExamples")
	expectStatus(t, generate(api, o, examplesPath(missingID), map[string]any{"targetAgent": "support_agent", "numExamples": 1}), http.StatusNotFound)
	expectDetail(t, generate(api, o, examplesPath("not-a-uuid"), map[string]any{"targetAgent": "support_agent", "numExamples": 1}), http.StatusBadRequest, "datasetId")

	op, opPath := expectAccepted(t, generate(api, o, examplesPath(other.ID), map[string]any{"targetAgent": "support_agent", "numExamples": 1}),
		datasetsPath+"/"+other.ID+"/operations")
	defer awaitOperation(api, opPath)
	for _, path := range []string{
		datasetsPath + "/" + fullID + "/operations/" + op.ID,
		datasetOperationsPath + "/" + op.ID,
		datasetOperationsPath + "/" + missingID,
		datasetsPath + "/" + missingID + "/operations",
	} {
		rec := api.call(http.MethodGet, path, nil)
		expectStatus(t, rec, http.StatusNotFound)
		decodeError(t, rec)
	}
	expectDetail(t, api.call(http.MethodGet, datasetOperationsPath+"/not-a-uuid", nil), http.StatusBadRequest, "id")
}

func TestExampleGeneration_FailureAddsNothing(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	o := newFakeOrchestrator(t, "")
	dataset := newDataset(api, "generation-failed-batch")
	newExample(api, dataset.ID, map[string]any{"prompt": "kept"})

	_, path := expectAccepted(t, generate(api, o, datasetsPath+"/"+dataset.ID+"/examples/generate",
		map[string]any{"targetAgent": "support_agent", "numExamples": 2}), datasetsPath+"/"+dataset.ID+"/operations")
	done, rec := awaitOperation(api, path)
	if done.Status != "failed" || done.Error == nil || strings.Contains(rec.Body.String(), orchestratorFault) {
		t.Fatalf("finished operation = %s, want failed without the orchestrator's text", rec.Body.String())
	}
	if got := decodeData[Dataset](t, api.call(http.MethodGet, datasetsPath+"/"+dataset.ID, nil)); got.ExampleCount != 1 {
		t.Errorf("exampleCount = %d after a failed batch, want 1", got.ExampleCount)
	}
}

func TestDatasetOperations_NewestFirstPagedAndSorted(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	o := newFakeOrchestrator(t, generatedExamples)
	dataset := newDataset(api, "generation-history")
	operationsPath := datasetsPath + "/" + dataset.ID + "/operations"
	var ids []string
	for range 3 {
		op, path := expectAccepted(t, generate(api, o, datasetsPath+"/"+dataset.ID+"/examples/generate",
			map[string]any{"targetAgent": "support_agent", "numExamples": 1}), operationsPath)
		awaitOperation(api, path)
		ids = append(ids, op.ID)
		time.Sleep(3 * time.Millisecond)
	}

	first := decodeList[Operation](t, api.call(http.MethodGet, operationsPath+"?pageSize=2&pageNumber=1", nil))
	second := decodeList[Operation](t, api.call(http.MethodGet, operationsPath+"?pageSize=2&pageNumber=2", nil))
	var got []string
	for _, op := range append(first.Data, second.Data...) {
		got = append(got, op.ID)
	}
	if want := []string{ids[2], ids[1], ids[0]}; !slices.Equal(got, want) {
		t.Errorf("operations = %v, want newest first %v", got, want)
	}
	if first.Meta.Pagination.Count != 3 || first.Meta.Pagination.TotalPages != 2 {
		t.Errorf("pagination = %+v, want count 3 over 2 pages", first.Meta.Pagination)
	}

	got = nil
	for _, op := range decodeList[Operation](t, api.call(http.MethodGet, operationsPath+"?sort=createdTime:asc", nil)).Data {
		got = append(got, op.ID)
	}
	if !slices.Equal(got, ids) {
		t.Errorf("sort=createdTime:asc = %v, want oldest first %v", got, ids)
	}
	expectDetail(t, api.call(http.MethodGet, operationsPath+"?sort=status:asc", nil), http.StatusUnprocessableEntity, "sort")
}

func TestDatasetDelete_TakesItsGenerationsAndExamples(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	o := newFakeOrchestrator(t, generatedExamples)
	dataset := newDataset(api, "generation-deleted")
	op, path := expectAccepted(t, generate(api, o, datasetsPath+"/"+dataset.ID+"/examples/generate",
		map[string]any{"targetAgent": "support_agent", "numExamples": 2}), datasetsPath+"/"+dataset.ID+"/operations")
	awaitOperation(api, path)
	example := newExample(api, dataset.ID, map[string]any{"prompt": "p"})

	expectStatus(t, api.call(http.MethodDelete, datasetsPath+"/"+dataset.ID, nil), http.StatusNoContent)
	for _, gone := range []string{
		datasetsPath + "/" + dataset.ID + "/operations/" + op.ID,
		datasetsPath + "/" + dataset.ID + "/operations",
		examplesPath + "/" + example.ID,
	} {
		expectStatus(t, api.call(http.MethodGet, gone, nil), http.StatusNotFound)
	}
}

func TestGenerationError_NotConfiguredIs501(t *testing.T) {
	err := generationError(&eval.FeatureDisabledError{Message: "AI dataset generation is not configured."})
	if ce, ok := errors.AsType[*CallerError](err); !ok || ce.Status != http.StatusNotImplemented {
		t.Errorf("generationError = %#v, want a 501 callerError", err)
	}
}

func TestGeneration_CallsOnlyTheHostTheRequestArrivedAt(t *testing.T) {
	for name, header := range map[string]string{"Origin": "Origin", "X-Forwarded-Host": "X-Forwarded-Host"} {
		t.Run(name, func(t *testing.T) {
			api := newEntrypointAPI(t, nil)
			named := newFakeOrchestrator(t, "[]")
			arrived := newFakeOrchestrator(t, "[]")

			req := newJSONRequest(api.t, http.MethodPut, datasetsPath+"/generate", map[string]any{"targetAgent": "Orchestrator", "numExamples": 1})
			req.Host = strings.TrimPrefix(arrived.URL, "http://")
			if header == "Origin" {
				req.Header.Set("Origin", named.URL)
			} else {
				req.Header.Set(header, strings.TrimPrefix(named.URL, "http://"))
			}
			req.Header.Set("Authorization", callerAuth)
			rec := httptest.NewRecorder()
			api.handler.ServeHTTP(rec, req)
			_, path := expectAccepted(t, rec, datasetOperationsPath)
			awaitOperation(api, path)

			if got := named.authHeaders(); len(got) != 0 {
				t.Errorf("the host named in %s received %d calls carrying the caller's Authorization, want none", header, len(got))
			}
			if got := arrived.authHeaders(); len(got) == 0 {
				t.Error("the host the request arrived at received no call")
			}
		})
	}
}

func TestGeneration_RecordsTheCallersPlatformUserID(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	o := newFakeOrchestrator(t, generatedDataset)
	asCaller := checkedAgainstSpec(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := auth.NewTestSubject(auth.SubjectFields{UserID: "alice@example.org", PlatformUserID: "alice-id", Authenticated: true, Scopes: []string{"*"}})
		api.core.Handler().ServeHTTP(w, r.WithContext(auth.ContextWithSubject(r.Context(), caller)))
	}))
	callerAPI := &entrypointAPI{apiClient: &apiClient{t: t, handler: asCaller, core: api.core}, gateway: api.gateway}

	started, path := expectAccepted(t, generate(callerAPI, o, datasetsPath+"/generate", map[string]any{"targetAgent": "support_agent", "numExamples": 2}), datasetOperationsPath)
	awaitOperation(callerAPI, path)
	op, err := store.NewEvalGenerationOperationRepo(api.core.DB()).Get(t.Context(), started.ID)
	if err != nil {
		t.Fatal(err)
	}
	var request struct {
		RequesterID string `json:"requesterId"`
	}
	if op.Request == nil {
		t.Fatal("the operation stored no request")
	}
	if err := json.Unmarshal([]byte(*op.Request), &request); err != nil || request.RequesterID != "alice-id" {
		t.Fatalf("stored request = %s, want requesterId alice-id", *op.Request)
	}
}
