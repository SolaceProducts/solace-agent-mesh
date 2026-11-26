// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const toolsetAPIKeySecret = "sk-toolset-secret-the-test-supplies"

type toolsetAPI struct {
	*entrypointAPI
	deps *entplatform.RouteDeps
}

func newToolsetAPI(t *testing.T) *toolsetAPI {
	t.Helper()
	return newToolsetAPIWithConfig(t, nil)
}

func newToolsetAPIWithConfig(t *testing.T, extra map[string]any) *toolsetAPI {
	t.Helper()
	core, deps := newTestCoreWithConfig(t, extra)
	return &toolsetAPI{
		entrypointAPI: &entrypointAPI{apiClient: newAPIClient(t, core)},
		deps:          deps,
	}
}

func (a *toolsetAPI) create(name string) Toolset {
	a.t.Helper()
	rec := a.call(http.MethodPost, "/toolsets", map[string]any{"name": name, "description": "Tools the test creates."})
	expectStatus(a.t, rec, http.StatusCreated)
	created := decodeData[Toolset](a.t, rec)
	expectLocation(a.t, rec, "/toolsets/"+created.ID)
	return created
}

func (a *entrypointAPI) upload(method, path, filename string, bundle []byte, scopes ...string) *httptest.ResponseRecorder {
	a.t.Helper()
	return uploadForm(a, method, path, filename, bundle, nil, scopes...)
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func (a *toolsetAPI) createReady(name string) Toolset {
	a.t.Helper()
	ts := a.create(name)
	expectStatus(a.t, a.upload(http.MethodPut, "/toolsets/"+ts.ID+"/bundle", name+".zip", zipOf(a.t, map[string]string{"tool.py": "print(1)"})), http.StatusOK)
	params := `{"type":"object"}`
	schema := `[{"key":"api_key","type":"string","secret":true,"required":true},{"key":"region","type":"string"}]`
	a.deps.ToolsetSvc.HandleToolInit(a.t.Context(), name+"__lookup", "Look up an order.", entplatform.ToolInitMeta{Params: &params, ConfigSchema: &schema})
	got := decodeData[Toolset](a.t, a.call(http.MethodGet, "/toolsets/"+ts.ID, nil))
	if got.DiscoveryStatus == nil || *got.DiscoveryStatus != "ready" {
		a.t.Fatalf("discoveryStatus %v after the tool runtime reported, want ready", got.DiscoveryStatus)
	}
	return got
}

func rawKeys(t *testing.T, rec *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	var body struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return body.Data
}

func TestToolsets_Lifecycle(t *testing.T) {
	api := newToolsetAPI(t)
	created := api.create("order_tools")
	if created.Type != "toolset" || created.Origin != originCustom || created.Name != "order_tools" {
		t.Errorf("created %+v", created)
	}
	if created.DiscoveryStatus == nil || *created.DiscoveryStatus != "created" || created.Bundle != nil {
		t.Errorf("a new toolset is created with no bundle, got status %v bundle %+v", created.DiscoveryStatus, created.Bundle)
	}
	iso := regexp.MustCompile(isoMillisUTC)
	if b, _ := json.Marshal(created.CreatedTime); !iso.Match(bytes.Trim(b, `"`)) {
		t.Errorf("createdTime %s is not ISO 8601 ms UTC", b)
	}

	rec := api.call(http.MethodGet, "/toolsets/"+created.ID, nil)
	expectStatus(t, rec, http.StatusOK)
	keys := rawKeys(t, rec)
	for _, dropped := range []string{"files", "zipFilename", "zipFileSize", "contentHash", "createdAt", "updatedAt"} {
		if _, ok := keys[dropped]; ok {
			t.Errorf("v1 field %q is on the v2 toolset", dropped)
		}
	}
	for _, null := range []string{"runtime", "bundle", "configSummary"} {
		if string(keys[null]) != "null" {
			t.Errorf("%s = %s before any upload, want null", null, keys[null])
		}
	}

	rec = api.call(http.MethodPatch, "/toolsets/"+created.ID, map[string]any{"name": "order_tools_v2"})
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[Toolset](t, rec); got.Name != "order_tools_v2" || got.Description == nil || *got.Description != "Tools the test creates." {
		t.Errorf("a rename keeps the description: %+v", got)
	}
	rec = api.call(http.MethodPatch, "/toolsets/"+created.ID, map[string]any{"description": nil})
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[Toolset](t, rec); got.Description != nil || got.Name != "order_tools_v2" {
		t.Errorf("description null clears only it: %+v", got)
	}

	expectStatus(t, api.call(http.MethodDelete, "/toolsets/"+created.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/toolsets/"+created.ID, nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodDelete, "/toolsets/"+created.ID, nil), http.StatusNotFound)
}

func TestToolsets_ListPutsBuiltinsFirstAndFiltersByOrigin(t *testing.T) {
	api := newToolsetAPI(t)
	custom := api.create("order_tools")

	all := decodeList[Toolset](t, api.call(http.MethodGet, "/toolsets?pageSize=100", nil))
	if len(all.Data) < 2 || all.Data[0].Origin != originBuiltin || all.Data[len(all.Data)-1].ID != custom.ID {
		t.Fatalf("want built-ins first and the custom toolset last, got %d items", len(all.Data))
	}
	if all.Meta.Pagination.Count != len(all.Data) {
		t.Errorf("count %d, want %d", all.Meta.Pagination.Count, len(all.Data))
	}

	customs := decodeList[Toolset](t, api.call(http.MethodGet, "/toolsets?origin=custom", nil))
	if len(customs.Data) != 1 || customs.Data[0].ID != custom.ID {
		t.Errorf("origin=custom listed %+v", customs.Data)
	}
	builtins := decodeList[Toolset](t, api.call(http.MethodGet, "/toolsets?origin=builtin&pageSize=100", nil))
	for _, b := range builtins.Data {
		if b.Origin != originBuiltin || b.DiscoveryStatus != nil || b.CreatedBy != nil {
			t.Errorf("built-in %s: origin %q, discoveryStatus %v, createdBy %v", b.ID, b.Origin, b.DiscoveryStatus, b.CreatedBy)
		}
	}
	if len(builtins.Data) != len(all.Data)-1 {
		t.Errorf("origin=builtin listed %d, want %d", len(builtins.Data), len(all.Data)-1)
	}
	byStatus := decodeList[Toolset](t, api.call(http.MethodGet, "/toolsets?discoveryStatus=ready", nil))
	if len(byStatus.Data) != 0 {
		t.Errorf("discoveryStatus=ready listed %d, want none", len(byStatus.Data))
	}

	for _, bad := range []string{"origin=platform", "discoveryStatus=created"} {
		field, _, _ := strings.Cut(bad, "=")
		expectDetail(t, api.call(http.MethodGet, "/toolsets?"+bad, nil), http.StatusUnprocessableEntity, field)
	}
}

func TestToolsets_Paging(t *testing.T) {
	api := newToolsetAPI(t)
	first := decodeList[Toolset](t, api.call(http.MethodGet, "/toolsets", nil))
	total := first.Meta.Pagination.Count
	if first.Meta.Pagination.PageSize != defaultPageSize || len(first.Data) != min(total, defaultPageSize) {
		t.Errorf("default page: size %d, %d items of %d", first.Meta.Pagination.PageSize, len(first.Data), total)
	}
	page2 := decodeList[Toolset](t, api.call(http.MethodGet, "/toolsets?pageSize=5&pageNumber=2", nil))
	if len(page2.Data) != min(5, max(0, total-5)) {
		t.Errorf("page 2 of 5 has %d items, total %d", len(page2.Data), total)
	}
	past := api.call(http.MethodGet, "/toolsets?pageNumber=1000", nil)
	if list := decodeList[Toolset](t, past); len(list.Data) != 0 {
		t.Errorf("a page past the end has %d items", len(list.Data))
	}
	expectStatus(t, api.call(http.MethodGet, "/toolsets?pageSize=100", nil), http.StatusOK)
	expectDetail(t, api.call(http.MethodGet, "/toolsets?pageSize=101", nil), http.StatusUnprocessableEntity, "pageSize")
	expectDetail(t, api.call(http.MethodGet, "/toolsets?pageSize=0", nil), http.StatusUnprocessableEntity, "pageSize")
	expectDetail(t, api.call(http.MethodGet, "/toolsets?pageNumber=ten", nil), http.StatusBadRequest, "pageNumber")
}

func TestToolsets_Validation(t *testing.T) {
	api := newToolsetAPI(t)
	expectDetail(t, api.call(http.MethodPost, "/toolsets", map[string]any{"description": "no name"}), http.StatusUnprocessableEntity, "name")
	expectDetail(t, api.call(http.MethodPost, "/toolsets", map[string]any{"name": "1bad name"}), http.StatusUnprocessableEntity, "name")
	expectDetail(t, api.call(http.MethodPost, "/toolsets", map[string]any{"name": "builtin_artifact_tools"}), http.StatusUnprocessableEntity, "name")
	expectDetail(t, api.call(http.MethodPost, "/toolsets", map[string]any{"name": "ok", "extra": 1}), http.StatusBadRequest, "extra")
	expectStatus(t, api.call(http.MethodPost, "/toolsets", `{"name":`), http.StatusBadRequest)

	ts := api.create("order_tools")
	expectStatus(t, api.call(http.MethodPost, "/toolsets", map[string]any{"name": "order_tools"}), http.StatusConflict)
	expectDetail(t, api.call(http.MethodPatch, "/toolsets/"+ts.ID, map[string]any{"name": nil}), http.StatusUnprocessableEntity, "name")
	expectDetail(t, api.call(http.MethodPatch, "/toolsets/"+ts.ID, map[string]any{"name": ""}), http.StatusUnprocessableEntity, "name")
	expectDetail(t, api.call(http.MethodPatch, "/toolsets/"+ts.ID, map[string]any{"values": map[string]any{}}), http.StatusBadRequest, "values")

	for _, bad := range []string{"Not-A-UUID", "a.b"} {
		expectDetail(t, api.call(http.MethodGet, "/toolsets/"+bad, nil), http.StatusBadRequest, "id")
		expectDetail(t, api.call(http.MethodDelete, "/toolsets/"+bad, nil), http.StatusBadRequest, "id")
	}
	expectStatus(t, api.call(http.MethodGet, "/toolsets/"+missingID, nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodPatch, "/toolsets/"+missingID, map[string]any{"name": "x"}), http.StatusNotFound)
}

func TestToolsets_BuiltinsAreFetchableAndFixed(t *testing.T) {
	api := newToolsetAPI(t)
	builtins := decodeList[Toolset](t, api.call(http.MethodGet, "/toolsets?origin=builtin&pageSize=100", nil))
	if len(builtins.Data) == 0 {
		t.Fatal("no built-in toolsets listed")
	}
	for _, b := range builtins.Data {
		rec := api.call(http.MethodGet, "/toolsets/"+b.ID, nil)
		expectStatus(t, rec, http.StatusOK)
		if got := decodeData[Toolset](t, rec); got.ID != b.ID || got.Origin != originBuiltin || len(got.Tools) == 0 {
			t.Errorf("GET %s = %+v", b.ID, got)
		}
	}

	const id = "builtin_artifact_tools"
	for _, write := range []*httptest.ResponseRecorder{
		api.call(http.MethodPatch, "/toolsets/"+id, map[string]any{"name": "mine"}),
		api.call(http.MethodDelete, "/toolsets/"+id, nil),
		api.call(http.MethodGet, "/toolsets/"+id+"/bundle", nil),
		api.upload(http.MethodPut, "/toolsets/"+id+"/bundle", "x.zip", zipOf(t, map[string]string{"a": "b"})),
		api.call(http.MethodPatch, "/toolsets/"+id+"/config", map[string]any{"values": map[string]any{}}),
	} {
		expectStatus(t, write, http.StatusConflict)
		decodeError(t, write)
	}

	cfg := api.call(http.MethodGet, "/toolsets/"+id+"/config", nil)
	expectStatus(t, cfg, http.StatusOK)
	if got := decodeData[ToolsetConfiguration](t, cfg); len(got.Values) != 0 || got.UpdatedBy != nil {
		t.Errorf("a built-in has no configuration, got %+v", got)
	}
	diff := api.call(http.MethodPut, "/toolsets/"+id+"/secretsDiff", map[string]any{"values": map[string]any{"k": "v"}})
	expectStatus(t, diff, http.StatusOK)
	if got := decodeData[SecretsDiffResult](t, diff); got.Secrets == nil || len(got.Secrets) != 0 {
		t.Errorf("a built-in has no secrets, got %+v", got)
	}

	for _, rec := range []*httptest.ResponseRecorder{
		api.call(http.MethodGet, "/toolsets/no_such_builtin", nil),
		api.call(http.MethodDelete, "/toolsets/no_such_builtin", nil),
		api.call(http.MethodGet, "/toolsets/no_such_builtin/config", nil),
	} {
		expectStatus(t, rec, http.StatusNotFound)
	}
}

func TestToolsets_Bundle(t *testing.T) {
	api := newToolsetAPI(t)
	ts := api.create("order_tools")
	expectStatus(t, api.call(http.MethodGet, "/toolsets/"+ts.ID+"/bundle", nil), http.StatusConflict)

	bundle := zipOf(t, map[string]string{"tool.py": "print(1)"})
	rec := api.upload(http.MethodPut, "/toolsets/"+ts.ID+"/bundle", "Order Tools.zip", bundle)
	expectStatus(t, rec, http.StatusOK)
	got := decodeData[Toolset](t, rec)
	if got.DiscoveryStatus == nil || *got.DiscoveryStatus != "pending" || got.Bundle == nil ||
		got.Bundle.Filename != "Order Tools.zip" || got.Bundle.SizeBytes == nil || *got.Bundle.SizeBytes != int64(len(bundle)) || got.Bundle.ContentHash == nil {
		t.Fatalf("after upload: status %v, bundle %+v", got.DiscoveryStatus, got.Bundle)
	}

	download := api.call(http.MethodGet, "/toolsets/"+ts.ID+"/bundle", nil)
	expectStatus(t, download, http.StatusOK)
	if !bytes.Equal(download.Body.Bytes(), bundle) || download.Header().Get("Content-Type") != "application/zip" ||
		download.Header().Get("Content-Disposition") != "attachment; filename*=UTF-8''Order%20Tools.zip" {
		t.Errorf("download: %d bytes, headers %v", download.Body.Len(), download.Header())
	}

	expectDetail(t, api.call(http.MethodPatch, "/toolsets/"+ts.ID, map[string]any{"name": "renamed"}), http.StatusUnprocessableEntity, "name")
	expectDetail(t, api.upload(http.MethodPut, "/toolsets/"+ts.ID+"/bundle", "tools.tar", bundle), http.StatusUnprocessableEntity, "file")
	notMultipart := api.call(http.MethodPut, "/toolsets/"+ts.ID+"/bundle", `{"file":"x"}`)
	expectStatus(t, notMultipart, http.StatusBadRequest)
	decodeError(t, notMultipart)

	notZip := api.upload(http.MethodPut, "/toolsets/"+ts.ID+"/bundle", "tools.zip", []byte("not a zip"))
	expectStatus(t, notZip, http.StatusUnprocessableEntity)
	body := decodeError(t, notZip)
	if problems := body.ValidationDetails["file"]; len(problems) != 1 || problems[0] != "invalid ZIP file" {
		t.Errorf("file details %v, want SAM's sentence only", body.ValidationDetails)
	}
	if strings.Contains(notZip.Body.String(), "zip:") {
		t.Errorf("the archive reader's own error reached the wire: %s", notZip.Body.String())
	}
	expectStatus(t, api.upload(http.MethodPut, "/toolsets/"+missingID+"/bundle", "x.zip", bundle), http.StatusNotFound)
}

func TestToolsets_ConfigurationAndSecrets(t *testing.T) {
	api := newToolsetAPI(t)
	pending := api.create("pending_tools")
	for _, rec := range []*httptest.ResponseRecorder{
		api.call(http.MethodGet, "/toolsets/"+pending.ID+"/config", nil),
		api.call(http.MethodPatch, "/toolsets/"+pending.ID+"/config", map[string]any{"values": map[string]any{}}),
		api.call(http.MethodPut, "/toolsets/"+pending.ID+"/secretsDiff", map[string]any{"values": map[string]any{}}),
	} {
		expectStatus(t, rec, http.StatusConflict)
		if msg := decodeError(t, rec).Message; msg != msgToolsetNotReady {
			t.Errorf("message %q, want %q", msg, msgToolsetNotReady)
		}
	}

	ready := api.createReady("order_tools")
	if ready.ConfigSummary == nil || !ready.ConfigSummary.RequiresAgentConfig || len(ready.Tools) != 1 || ready.Tools[0].ToolName != "lookup" {
		t.Fatalf("ready toolset: summary %+v, tools %+v", ready.ConfigSummary, ready.Tools)
	}

	empty := api.call(http.MethodGet, "/toolsets/"+ready.ID+"/config", nil)
	expectStatus(t, empty, http.StatusOK)
	if keys := rawKeys(t, empty); string(keys["values"]) != "{}" || string(keys["updatedBy"]) != "null" || string(keys["updatedTime"]) != "null" {
		t.Errorf("unset configuration = %s", empty.Body.String())
	}

	rec := api.call(http.MethodPatch, "/toolsets/"+ready.ID+"/config", map[string]any{
		"values": map[string]any{"lookup": map[string]any{"api_key": toolsetAPIKeySecret, "region": "us-east-1"}},
	})
	expectStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), toolsetAPIKeySecret) {
		t.Fatalf("the secret came back: %s", rec.Body.String())
	}
	cfg := decodeData[ToolsetConfiguration](t, rec)
	tool, _ := cfg.Values["lookup"].(map[string]any)
	if tool["api_key"] != redacted || tool["region"] != "us-east-1" || cfg.UpdatedBy == nil || cfg.UpdatedTime == 0 {
		t.Errorf("configuration after patch: %+v", cfg)
	}

	kept := api.call(http.MethodPatch, "/toolsets/"+ready.ID+"/config", map[string]any{"values": map[string]any{"lookup": map[string]any{"region": nil}}})
	expectStatus(t, kept, http.StatusOK)
	tool, _ = decodeData[ToolsetConfiguration](t, kept).Values["lookup"].(map[string]any)
	if _, ok := tool["region"]; ok || tool["api_key"] != redacted {
		t.Errorf("null clears region and keeps the secret, got %v", tool)
	}
	expectDetail(t, api.call(http.MethodPatch, "/toolsets/"+ready.ID+"/config", map[string]any{"values": nil}), http.StatusUnprocessableEntity, "values")
	unchanged := api.call(http.MethodPatch, "/toolsets/"+ready.ID+"/config", map[string]any{})
	expectStatus(t, unchanged, http.StatusOK)
	if strings.Contains(unchanged.Body.String(), toolsetAPIKeySecret) {
		t.Errorf("the secret came back: %s", unchanged.Body.String())
	}

	diff := api.call(http.MethodPut, "/toolsets/"+ready.ID+"/secretsDiff", map[string]any{"values": map[string]any{"api_key": toolsetAPIKeySecret}})
	expectStatus(t, diff, http.StatusOK)
	if got := decodeData[SecretsDiffResult](t, diff); !got.Secrets["api_key"].Matches {
		t.Errorf("the stored secret should match: %+v", got)
	}
	changed := decodeData[SecretsDiffResult](t, api.call(http.MethodPut, "/toolsets/"+ready.ID+"/secretsDiff", map[string]any{"values": map[string]any{"api_key": "sk-other"}}))
	if changed.Secrets["api_key"].Matches {
		t.Errorf("a different secret should not match: %+v", changed)
	}
	expectDetail(t, api.call(http.MethodPut, "/toolsets/"+ready.ID+"/secretsDiff", map[string]any{}), http.StatusUnprocessableEntity, "values")
	expectStatus(t, api.call(http.MethodPut, "/toolsets/"+missingID+"/secretsDiff", map[string]any{"values": map[string]any{}}), http.StatusNotFound)
}

func TestToolsets_Scopes(t *testing.T) {
	api := newToolsetAPI(t)
	ts := api.create("order_tools")
	read := rbac.ScopeToolsetRead
	expectStatus(t, api.call(http.MethodGet, "/toolsets", nil, read), http.StatusOK)
	for _, sub := range []string{"", "/bundle", "/config"} {
		path := "/toolsets/" + ts.ID + sub
		if rec := api.call(http.MethodGet, path, nil, read); rec.Code == http.StatusForbidden {
			t.Errorf("GET %s with %s = 403, want it allowed", path, read)
		}
		if rec := api.call(http.MethodGet, path, nil, rbac.ScopeSkillRead); rec.Code != http.StatusForbidden {
			t.Errorf("GET %s with only %s = %d, want 403", path, rbac.ScopeSkillRead, rec.Code)
		}
	}
	for _, rec := range []*httptest.ResponseRecorder{
		api.call(http.MethodPost, "/toolsets", map[string]any{"name": "other"}, read),
		api.call(http.MethodPatch, "/toolsets/"+ts.ID, map[string]any{"name": "other"}, read),
		api.call(http.MethodDelete, "/toolsets/"+ts.ID, nil, read),
		api.upload(http.MethodPut, "/toolsets/"+ts.ID+"/bundle", "x.zip", zipOf(t, map[string]string{"a": "b"}), read),
		api.call(http.MethodPatch, "/toolsets/"+ts.ID+"/config", map[string]any{"values": map[string]any{}}, read),
		api.call(http.MethodPut, "/toolsets/"+ts.ID+"/secretsDiff", map[string]any{"values": map[string]any{}}, read),
		api.call(http.MethodGet, "/toolsets", nil, rbac.ScopeSkillRead),
	} {
		expectStatus(t, rec, http.StatusForbidden)
	}
}
