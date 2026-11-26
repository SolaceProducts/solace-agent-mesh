// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type Example struct {
	ID               string           `json:"id" example:"0190f5e9-8a9b-7c0d-9e1f-2a3b4c5d6e7f"`
	Type             string           `json:"type" example:"example"`
	DatasetID        string           `json:"datasetId" example:"0190f5e9-3c4d-7e5f-8a6b-7c8d9e0f1a2b"`
	SequenceNumber   int              `json:"sequenceNumber" example:"3"`
	Prompt           string           `json:"prompt" example:"What is 2 + 2?"`
	ExpectedResponse *string          `json:"expectedResponse" example:"4"`
	InputData        JSONObject       `json:"inputData" example:"{\"orderId\":\"O-5003\",\"rush\":true}"`
	ExpectedPath     *ExpectedPath    `json:"expectedPath"`
	ExpectedFacts    *ExpectedFacts   `json:"expectedFacts"`
	InputArtifact    *ExampleArtifact `json:"inputArtifact"`
	ExpectedArtifact *ExampleArtifact `json:"expectedArtifact"`
	CreatedTime      Instant          `json:"createdTime"`
	UpdatedTime      Instant          `json:"updatedTime"`
}

func (e Example) Location() string { return platpkg.APIV2Prefix + examplePathPrefix + e.ID }

type ExampleArtifact struct {
	Filename    string `json:"filename" example:"policy.pdf"`
	MimeType    string `json:"mimeType" example:"application/pdf"`
	Size        int64  `json:"size" example:"20480"`
	Version     int    `json:"version" example:"0"`
	ContentHash string `json:"contentHash" example:"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"`
}

type ExpectedPath struct {
	Nodes []ExpectedNode `json:"nodes" example:"[{\"nodeId\":\"approve\",\"expectation\":\"succeeded\"},{\"nodeId\":\"escalate\",\"expectation\":\"not_ran\"}]"`
	Tools []ExpectedTool `json:"tools" example:"[{\"toolName\":\"issue_refund\",\"expectation\":\"called\"}]"`
}

type ExpectedFacts struct {
	Required  []string `json:"required" example:"[\"O-1\",\"refund approved\"]"`
	Forbidden []string `json:"forbidden" example:"[\"escalated\"]"`
}

type ExpectedNode struct {
	NodeID      string `json:"nodeId" example:"approve"`
	Expectation string `json:"expectation" example:"succeeded"`
}

type ExpectedTool struct {
	ToolName    string `json:"toolName" example:"issue_refund"`
	Expectation string `json:"expectation" example:"called"`
}

type ImportExamplesResult struct {
	Created int `json:"created" example:"20"`
	Skipped int `json:"skipped" example:"1"`
	Dropped int `json:"dropped" example:"0"`
	Limit   int `json:"limit" example:"5000"`
}

type CreateExampleRequest struct {
	DatasetID        string         `path:"datasetId" json:"-"`
	Prompt           string         `json:"prompt" example:"What is 2 + 2?"`
	ExpectedResponse *string        `json:"expectedResponse" example:"4"`
	InputData        JSONObject     `json:"inputData" example:"{\"orderId\":\"O-5003\",\"rush\":true}"`
	ExpectedPath     *ExpectedPath  `json:"expectedPath"`
	ExpectedFacts    *ExpectedFacts `json:"expectedFacts"`
	SequenceNumber   *int           `json:"sequenceNumber" example:"3"`
}

type UpdateExampleRequest struct {
	ID               string               `path:"id" json:"-"`
	Prompt           Patch[string]        `json:"prompt" example:"What is 3 + 3?"`
	ExpectedResponse Patch[string]        `json:"expectedResponse" clearable:"true" example:"6"`
	InputData        Patch[JSONObject]    `json:"inputData" clearable:"true" example:"{\"orderId\":\"O-5003\",\"rush\":true}"`
	ExpectedPath     Patch[ExpectedPath]  `json:"expectedPath" clearable:"true"`
	ExpectedFacts    Patch[ExpectedFacts] `json:"expectedFacts" clearable:"true"`
	SequenceNumber   Patch[int]           `json:"sequenceNumber" example:"3"`
	DatasetID        Patch[string]        `json:"datasetId" example:"0190f5e9-3c4d-7e5f-8a6b-7c8d9e0f1a2b"`
}

type listExamplesRequest struct {
	DatasetID string    `path:"datasetId" json:"-"`
	Page      Page      `json:"-"`
	Sort      SortOrder `sort:"sequenceNumber,createdTime,updatedTime" json:"-"`
	Content   string    `query:"content" json:"-" example:"refund"`
}

type importExamplesRequest struct {
	DatasetID string       `path:"datasetId" json:"-"`
	File      UploadedFile `form:"file" json:"-"`
}

type exportExamplesRequest struct {
	DatasetID string `path:"datasetId" json:"-"`
	Format    string `query:"format" json:"-" example:"csv"`
}

type exampleIDRequest struct {
	ExampleID string `path:"exampleId" json:"-"`
}

type setExampleArtifactRequest struct {
	ExampleID   string       `path:"exampleId" json:"-"`
	File        UploadedFile `form:"file" json:"-"`
	Description string       `form:"description" json:"-" example:"The order history the agent should read"`
}

type exampleArtifactRequest struct {
	ExampleID string `path:"exampleId" json:"-"`
	Filename  string `path:"filename,text" json:"-"`
	Version   string `query:"version" json:"-" example:"0"`
}

const (
	exampleResource       = "example"
	examplesOfDatasetPath = datasetIDPath + "/examples"
	examplePathPrefix     = evaluationsPath + "/examples/"
	examplePath           = examplePathPrefix + "{id}"
	exampleByIDPath       = examplePathPrefix + "{exampleId}"
)

func exampleRoutes(h *evaluationHandler) []Route {
	routes := []Route{
		{Method: http.MethodGet, Path: examplesOfDatasetPath, Scope: rbac.ScopeEvaluationRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "listExamples", Summary: "Get a list of examples in an evaluation dataset",
				Text: "Returns one page of the dataset's examples in sequenceNumber order, optionally only those whose prompt or expected response contains some text. If the dataset does not exist, the request fails with 404.", Tag: examplesTag},
			Handler: Handle(h.listExamples, "Unable to list examples")},
		{Method: http.MethodPost, Path: examplesOfDatasetPath, Scope: rbac.ScopeEvaluationCreate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "createExample", Summary: "Create an evaluation example",
				Text: "Adds an example to the dataset and returns a Location header that points to it. The example needs a prompt, an inputData object, or both. The request fails with 422 if the dataset already holds its limit of examples, if another example has the sequenceNumber, or if inputData, expectedPath, or expectedFacts is set while workflow evaluations are turned off.", Tag: examplesTag},
			Handler: Handle(h.createExample, "Unable to create example")},
		{Method: http.MethodGet, Path: examplePath, Scope: rbac.ScopeEvaluationRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getExample", Summary: "Get an evaluation example",
				Text: "Returns one example with the metadata of its input and expected files.", Tag: examplesTag},
			Handler: Handle(h.getExample, "Unable to get example")},
		{Method: http.MethodPatch, Path: examplePath, Scope: rbac.ScopeEvaluationUpdate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "updateExample", Summary: "Update an evaluation example",
				Text: "Changes the prompt, expected response, inputData, expectedPath, and expectedFacts the body includes and keeps the rest. A null expectedPath or expectedFacts clears it. If the body includes sequenceNumber or datasetId, the request fails with 422, because both are fixed when the example is created.", Tag: examplesTag},
			Handler: Handle(h.updateExample, "Unable to update example")},
		{Method: http.MethodDelete, Path: examplePath, Scope: rbac.ScopeEvaluationDelete, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "deleteExample", Summary: "Delete an evaluation example",
				Text: "Deletes the example with its files and the run results recorded for it.", Tag: examplesTag},
			Handler: Handle(h.deleteExample, "Unable to delete example")},
		{Method: http.MethodPut, Path: examplesOfDatasetPath + "/import", Scope: rbac.ScopeEvaluationCreate,
			Stability: StabilityDraft,
			Doc: Doc{ID: "importExamples", Summary: "Initiate an import of evaluation examples from a file",
				Text: "Adds the examples in an uploaded .csv or .json file (multipart field file) after the dataset's existing ones, in file order. A CSV has a header row with a prompt column, an input_data column, or both. It can also have expected_response, expected_path, and expected_facts columns. Other columns are ignored. A JSON file is an array of objects with the keys prompt, expectedResponse, inputData, expectedPath, expectedFacts, and sequenceNumber, the shape the export writes. The snake_case keys expected_response, sequence_number, input_data, expected_path, and expected_facts are read too. Exports before 2026-05-07 used the first two. The sequenceNumber key is ignored, and any other key is refused. An input_data cell or inputData value is a workflow example's structured input: a JSON object, or a blank cell or null for none. An expected_path cell or expectedPath value is the example's expected path in the file form: an object of maps from name to expectation, such as {\"nodes\":{\"approve\":\"succeeded\"},\"tools\":{\"issue_refund\":\"called\"}}. The file form differs from the lists this API's examples carry. An expected_facts cell or expectedFacts value is the example's expected facts in the same form this API uses, such as {\"required\":[\"O-1\"],\"forbidden\":[\"escalated\"]}. For a path or facts, a blank cell or null means none. If the file has any input, path, or facts while workflow evaluations are turned off, the request fails with 422. A leading UTF-8 byte order mark is ignored. Rows are numbered from 1, not counting the header. A row with neither a prompt nor an input is skipped. Rows past the dataset's limit are dropped. The result counts both. The import is all or nothing. If the file is malformed, the request fails with 422 naming the row, and nothing is added.", Tag: examplesTag,
				Errors: []int{http.StatusRequestEntityTooLarge}},
			Handler: Handle(h.importExamples, "Unable to import examples")},
		{Method: http.MethodGet, Path: examplesOfDatasetPath + "/export", Scope: rbac.ScopeEvaluationRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "exportExamples", Summary: "Get an export of the examples of an evaluation dataset",
				Text: "Downloads every example of the dataset in sequenceNumber order. With format=csv, the default, the file has the columns sequence_number, prompt, and expected_response. The columns input_data, expected_path, and expected_facts follow, each only when any example has one. With format=json, the file is an array of objects with the keys sequenceNumber, prompt, and expectedResponse, which is null when the example has none. The keys inputData, expectedPath, and expectedFacts appear only when the example has them. A path is written in the file form the import reads: an object of maps from name to expectation. Either file can be imported again. Files attached to the examples are not exported.", Tag: examplesTag},
			Handler: Handle(h.exportExamples, "Unable to export examples")},
	}
	routes = append(routes, exampleArtifactRoutes(h, exampleSlot{
		path: "inputArtifact", role: store.EvalArtifactRoleInput, idSuffix: "InputArtifact", noun: "input file",
		uploadText: "The agent receives the file with the prompt when the example runs.",
	})...)
	return append(routes, exampleArtifactRoutes(h, exampleSlot{
		path: "expectedArtifact", role: store.EvalArtifactRoleExpected, idSuffix: "ExpectedArtifact", noun: "expected file",
		uploadText: "The evaluators read the file as the reference answer, and the agent never receives it. The file must be text, such as Markdown, JSON, or CSV. If it is any other type, the request fails with 422.",
	})...)
}

const exampleFileSummarySuffix = " of an evaluation example"

type exampleSlot struct {
	path       string
	role       string
	idSuffix   string
	noun       string
	uploadText string
}

func exampleArtifactRoutes(h *evaluationHandler, slot exampleSlot) []Route {
	slotPath := exampleByIDPath + "/" + slot.path
	return []Route{
		{Method: http.MethodPut, Path: slotPath, Scope: rbac.ScopeEvaluationCreate,
			Stability: StabilityDraft,
			Doc: Doc{ID: "setExample" + slot.idSuffix, Summary: "Upload the " + slot.noun + exampleFileSummarySuffix,
				Text: "Stores the uploaded file (multipart field file, with an optional description) as the example's " + slot.noun + ", replacing and deleting the one it had. " + slot.uploadText + " Uploading the same file again changes nothing. The request fails with 422 if Agent Mesh has no artifact service, the file is unsafe or not an allowed type, or the dataset's files would exceed their total size limit. If the file is over the size limit, the request fails with 413.", Tag: examplesTag, Errors: []int{http.StatusRequestEntityTooLarge}},
			MaxBodyBytes: h.artifacts.MaxBytes() + multipartEnvelopeSlack,
			Handler:      Handle(h.setExampleArtifact(slot.role), "Unable to upload "+slot.noun)},
		{Method: http.MethodGet, Path: slotPath + "/{filename...}", Scope: rbac.ScopeEvaluationRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "getExample" + slot.idSuffix, Summary: "Get the " + slot.noun + exampleFileSummarySuffix,
				Text: "Downloads the example's " + slot.noun + " as an attachment, in the version the example lists. If the example has no such file, or the version is not the one listed, the request fails with 404.", Tag: examplesTag},
			Handler: Handle(h.getExampleArtifact(slot.role), "Unable to get "+slot.noun)},
		{Method: http.MethodDelete, Path: slotPath, Scope: rbac.ScopeEvaluationDelete,
			Stability: StabilityDraft,
			Doc: Doc{ID: "deleteExample" + slot.idSuffix, Summary: "Delete the " + slot.noun + exampleFileSummarySuffix,
				Text: "Deletes the example's " + slot.noun + " and returns 204, also when it had none.", Tag: examplesTag},
			Handler: Handle(h.deleteExampleArtifact(slot.role), "Unable to delete "+slot.noun)},
	}
}

var exampleSortFields = map[string]store.EvalExampleSortField{
	"sequenceNumber": store.EvalExampleSortSequenceNumber,
	"createdTime":    store.EvalExampleSortCreatedTime,
	"updatedTime":    store.EvalExampleSortUpdatedTime,
}

func (h *evaluationHandler) listExamples(ctx context.Context, req listExamplesRequest) (List[Example], error) {
	resp, err := h.examples.ListPaginated(ctx, req.DatasetID, req.Page.Number, req.Page.Size, req.Content,
		exampleSortFields[req.Sort.Field], req.Sort.Desc)
	if err != nil {
		return List[Example]{}, err
	}
	items := make([]Example, len(resp.Data))
	for i := range resp.Data {
		items[i] = toExample(&resp.Data[i])
	}
	return List[Example]{Items: items, Total: resp.Meta.Pagination.Count}, nil
}

func (h *evaluationHandler) createExample(ctx context.Context, req CreateExampleRequest) (Example, error) {
	path, err := toEvalExpectedPath(req.ExpectedPath)
	if err != nil {
		return Example{}, err
	}
	resp, err := h.examples.Create(ctx, req.DatasetID, eval.CreateExampleRequest{
		Prompt: req.Prompt, ExpectedResponse: req.ExpectedResponse, SequenceNumber: req.SequenceNumber,
		InputData: json.RawMessage(req.InputData), ExpectedPath: path, ExpectedFacts: toEvalExpectedFacts(req.ExpectedFacts),
	})
	if err != nil {
		return Example{}, camelFieldError(err)
	}
	return toExample(resp), nil
}

func (h *evaluationHandler) getExample(ctx context.Context, req IDRequest) (Example, error) {
	resp, err := h.examples.Get(ctx, req.ID)
	if err != nil {
		return Example{}, err
	}
	return toExample(resp), nil
}

func (h *evaluationHandler) updateExample(ctx context.Context, req UpdateExampleRequest) (Example, error) {
	details := map[string][]string{}
	prompt, err := h.patchedPrompt(ctx, details, req)
	if err != nil {
		return Example{}, err
	}
	if req.SequenceNumber.Set {
		details["sequenceNumber"] = []string{"cannot be changed"}
	}
	if req.DatasetID.Set {
		details["datasetId"] = []string{"cannot be changed"}
	}
	if len(details) > 0 {
		return Example{}, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed, Details: details}
	}
	update := eval.UpdateExampleRequest{Prompt: prompt}
	update.ExpectedResponse, update.ExpectedResponseSet = patchPointer(req.ExpectedResponse)
	if req.InputData.Set {
		update.InputData, update.InputDataSet = json.RawMessage(req.InputData.Value), true
	}
	if req.ExpectedPath.Set {
		update.ExpectedPathSet = true
		if !req.ExpectedPath.Null {
			if update.ExpectedPath, err = toEvalExpectedPath(&req.ExpectedPath.Value); err != nil {
				return Example{}, err
			}
		}
	}
	facts, factsSet := patchPointer(req.ExpectedFacts)
	update.ExpectedFacts, update.ExpectedFactsSet = toEvalExpectedFacts(facts), factsSet
	resp, err := h.examples.Update(ctx, req.ID, update)
	if err != nil {
		return Example{}, camelFieldError(err)
	}
	return toExample(resp), nil
}

func (h *evaluationHandler) patchedPrompt(ctx context.Context, details map[string][]string, req UpdateExampleRequest) (*string, error) {
	if !req.Prompt.Set || req.Prompt.Null || strings.TrimSpace(req.Prompt.Value) != "" {
		prompt, _ := RequiredPatch(details, "prompt", req.Prompt)
		return prompt, nil
	}
	keepsInput := req.InputData.Set && !req.InputData.Null
	if !req.InputData.Set {
		current, err := h.examples.Get(ctx, req.ID)
		if err != nil {
			return nil, err
		}
		keepsInput = current.InputData != nil
	}
	if !keepsInput {
		details["prompt"] = []string{"cannot be empty unless the example has inputData"}
		return nil, nil
	}
	return &req.Prompt.Value, nil
}

func (h *evaluationHandler) deleteExample(ctx context.Context, req IDRequest) (struct{}, error) {
	return struct{}{}, h.examples.Delete(ctx, req.ID)
}

func (h *evaluationHandler) importExamples(ctx context.Context, req importExamplesRequest) (ImportExamplesResult, error) {
	format, ok := importFormatOf(req.File.Name)
	if !ok {
		return ImportExamplesResult{}, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed,
			Details: map[string][]string{"file": {"must be a .csv or .json file"}}}
	}
	result, err := h.datasets.ImportExamplesStrict(ctx, req.DatasetID, format, req.File.Data)
	if err != nil {
		return ImportExamplesResult{}, camelFieldError(err)
	}
	return ImportExamplesResult(*result), nil
}

func importFormatOf(filename string) (eval.ImportFormat, bool) {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".csv":
		return eval.ImportFormatCSV, true
	case ".json":
		return eval.ImportFormatJSON, true
	}
	return "", false
}

func (h *evaluationHandler) exportExamples(ctx context.Context, req exportExamplesRequest) (Download, error) {
	format := req.Format
	if format == "" {
		format = formatCSV
	}
	filename := fmt.Sprintf("dataset-%s-examples.%s", req.DatasetID, format)
	switch format {
	case formatJSON:
		records, err := h.datasets.ExportExamplesJSON(ctx, req.DatasetID)
		if err != nil {
			return Download{}, err
		}
		body, err := json.MarshalIndent(records, "", "  ")
		if err != nil {
			return Download{}, fmt.Errorf("encode examples: %w", err)
		}
		return Download{Content: body, MimeType: "application/json", Filename: filename}, nil
	case formatCSV:
		text, err := h.datasets.ExportExamplesCSV(ctx, req.DatasetID)
		if err != nil {
			return Download{}, err
		}
		return Download{Content: []byte(text), MimeType: "text/csv", Filename: filename}, nil
	default:
		return Download{}, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgInvalidQueryParam,
			Details: map[string][]string{"format": {"must be csv or json"}}}
	}
}

func (h *evaluationHandler) setExampleArtifact(role string) func(context.Context, setExampleArtifactRequest) (ExampleArtifact, error) {
	return func(ctx context.Context, req setExampleArtifactRequest) (ExampleArtifact, error) {
		resp, err := h.artifacts.Set(ctx, req.ExampleID, role, req.File.Name, req.File.ContentType, req.File.Data, req.Description)
		if err != nil {
			return ExampleArtifact{}, exampleFileError(err)
		}
		return *toExampleFile(resp), nil
	}
}

func (h *evaluationHandler) getExampleArtifact(role string) func(context.Context, exampleArtifactRequest) (Download, error) {
	return func(ctx context.Context, req exampleArtifactRequest) (Download, error) {
		version, err := optionalVersion(req.Version)
		if err != nil {
			return Download{}, err
		}
		content, mimeType, err := h.artifacts.Get(ctx, req.ExampleID, role, req.Filename, version)
		if err != nil {
			return Download{}, err
		}
		return Download{Content: content, MimeType: mimeType, Filename: req.Filename}, nil
	}
}

func (h *evaluationHandler) deleteExampleArtifact(role string) func(context.Context, exampleIDRequest) (struct{}, error) {
	return func(ctx context.Context, req exampleIDRequest) (struct{}, error) {
		return struct{}{}, h.artifacts.Clear(ctx, req.ExampleID, role)
	}
}

var fileRefusals = []string{eval.CodeExampleArtifactTooLarge, eval.CodeExpectedArtifactNotText, eval.CodeDatasetArtifactTotalCap}

func exampleFileError(err error) error {
	if rule, ok := errors.AsType[*apierr.BusinessRuleViolationError](err); ok && slices.Contains(fileRefusals, rule.Code) {
		if rule.Code == eval.CodeExampleArtifactTooLarge {
			return &CallerError{Status: http.StatusRequestEntityTooLarge, Message: msgBodyTooLarge,
				Details: map[string][]string{"file": {rule.Message}}, Cause: err}
		}
		return &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed,
			Details: map[string][]string{"file": {rule.Message}}, Cause: err}
	}
	return camelFieldError(err)
}

func toEvalExpectedPath(path *ExpectedPath) (*eval.ExpectedPath, error) {
	if path == nil {
		return nil, nil
	}
	out := &eval.ExpectedPath{}
	details := map[string][]string{}
	out.Nodes = expectationsByName(details, "expectedPath.nodes", "node", path.Nodes, func(n ExpectedNode) (string, string) { return n.NodeID, n.Expectation })
	out.Tools = expectationsByName(details, "expectedPath.tools", "tool", path.Tools, func(t ExpectedTool) (string, string) { return t.ToolName, t.Expectation })
	if len(details) > 0 {
		return nil, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed, Details: details}
	}
	return out, nil
}

func expectationsByName[T any](details map[string][]string, field, noun string, entries []T, split func(T) (string, string)) map[string]string {
	if len(entries) == 0 {
		return nil
	}
	byName := make(map[string]string, len(entries))
	for _, entry := range entries {
		name, expectation := split(entry)
		if _, twice := byName[name]; twice {
			details[field] = append(details[field], fmt.Sprintf("names %s %q more than once", noun, name))
		}
		byName[name] = expectation
	}
	return byName
}

func fromEvalExpectedPath(path *eval.ExpectedPath) *ExpectedPath {
	if path == nil {
		return nil
	}
	out := &ExpectedPath{Nodes: make([]ExpectedNode, 0, len(path.Nodes)), Tools: make([]ExpectedTool, 0, len(path.Tools))}
	for _, id := range slices.Sorted(maps.Keys(path.Nodes)) {
		out.Nodes = append(out.Nodes, ExpectedNode{NodeID: id, Expectation: path.Nodes[id]})
	}
	for _, name := range slices.Sorted(maps.Keys(path.Tools)) {
		out.Tools = append(out.Tools, ExpectedTool{ToolName: name, Expectation: path.Tools[name]})
	}
	return out
}

func toEvalExpectedFacts(facts *ExpectedFacts) *eval.ExpectedFacts {
	if facts == nil {
		return nil
	}
	return &eval.ExpectedFacts{Required: facts.Required, Forbidden: facts.Forbidden}
}

func fromEvalExpectedFacts(facts *eval.ExpectedFacts) *ExpectedFacts {
	if facts == nil {
		return nil
	}
	return &ExpectedFacts{Required: nonNilList(facts.Required), Forbidden: nonNilList(facts.Forbidden)}
}

func toExample(resp *eval.ExampleResponse) Example {
	return Example{
		ID:               resp.ID,
		Type:             exampleResource,
		DatasetID:        resp.DatasetID,
		SequenceNumber:   resp.SequenceNumber,
		Prompt:           resp.Prompt,
		ExpectedResponse: NullIfEmpty(resp.ExpectedResponse),
		InputData:        JSONObject(resp.InputData),
		ExpectedPath:     fromEvalExpectedPath(resp.ExpectedPath),
		ExpectedFacts:    fromEvalExpectedFacts(resp.ExpectedFacts),
		InputArtifact:    toExampleFile(resp.InputArtifact),
		ExpectedArtifact: toExampleFile(resp.ExpectedArtifact),
		CreatedTime:      Instant(resp.CreatedTime),
		UpdatedTime:      Instant(resp.UpdatedTime),
	}
}

func toExampleFile(a *eval.ExampleArtifactResponse) *ExampleArtifact {
	if a == nil {
		return nil
	}
	return &ExampleArtifact{Filename: a.Filename, MimeType: a.MimeType, Size: a.Size, Version: a.Version, ContentHash: a.ContentHash}
}
