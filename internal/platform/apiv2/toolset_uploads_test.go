// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func (a *entrypointAPI) putBytes(path string, data []byte, scopes ...string) *httptest.ResponseRecorder {
	a.t.Helper()
	req := httptest.NewRequest(http.MethodPut, platpkg.APIV2Prefix+path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/octet-stream")
	if len(scopes) > 0 {
		req.Header.Set("X-SAM-Scopes", strings.Join(scopes, ","))
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	return rec
}

func (a *toolsetAPI) initUpload(toolsetID string, totalSize int) ToolsetUpload {
	a.t.Helper()
	rec := a.call(http.MethodPost, "/toolsetUploads", map[string]any{"toolsetId": toolsetID, "filename": "order_tools.zip", "totalSize": totalSize})
	expectStatus(a.t, rec, http.StatusCreated)
	up := decodeData[ToolsetUpload](a.t, rec)
	expectLocation(a.t, rec, "/toolsetUploads/"+up.ID)
	return up
}

func TestToolsetUploads_HappyPath(t *testing.T) {
	api := newToolsetAPI(t)
	ts := api.create("order_tools")
	bundle := zipOf(t, map[string]string{"tool.py": "print(1)"})

	up := api.initUpload(ts.ID, len(bundle))
	if up.Type != "toolsetUpload" || up.ChunkSize <= 0 || up.ToolsetID != ts.ID || up.Filename != "order_tools.zip" || up.TotalSize != int64(len(bundle)) {
		t.Fatalf("created upload %+v", up)
	}

	rec := api.call(http.MethodGet, "/toolsetUploads/"+up.ID, nil)
	expectStatus(t, rec, http.StatusOK)
	if string(rawKeys(t, rec)["receivedChunks"]) != "[]" {
		t.Errorf("receivedChunks = %s before any chunk, want []", rawKeys(t, rec)["receivedChunks"])
	}

	chunkSize := int(up.ChunkSize)
	for i := 0; i*chunkSize < len(bundle); i++ {
		end := min((i+1)*chunkSize, len(bundle))
		expectStatus(t, api.putBytes("/toolsetUploads/"+up.ID+"/chunks/"+strconv.Itoa(i), bundle[i*chunkSize:end]), http.StatusNoContent)
	}

	got := decodeData[ToolsetUpload](t, api.call(http.MethodGet, "/toolsetUploads/"+up.ID, nil))
	if len(got.ReceivedChunks) != 1 || got.ReceivedChunks[0] != 0 || got.ReceivedBytes != int64(len(bundle)) {
		t.Errorf("after the chunk: %+v", got)
	}

	done := api.call(http.MethodPut, "/toolsetUploads/"+up.ID+"/complete", nil)
	expectStatus(t, done, http.StatusOK)
	completed := decodeData[Toolset](t, done)
	if completed.ID != ts.ID || completed.DiscoveryStatus == nil || *completed.DiscoveryStatus != "pending" ||
		completed.Bundle == nil || completed.Bundle.SizeBytes == nil || *completed.Bundle.SizeBytes != int64(len(bundle)) {
		t.Errorf("completed toolset %+v", completed)
	}

	expectStatus(t, api.call(http.MethodGet, "/toolsetUploads/"+up.ID, nil), http.StatusNotFound)
	download := api.call(http.MethodGet, "/toolsets/"+ts.ID+"/bundle", nil)
	expectStatus(t, download, http.StatusOK)
	if !bytes.Equal(download.Body.Bytes(), bundle) {
		t.Errorf("downloaded %d bytes, want the %d uploaded", download.Body.Len(), len(bundle))
	}
}

func TestToolsetUploads_ReplacingAChunkKeepsTheTotal(t *testing.T) {
	api := newToolsetAPI(t)
	ts := api.create("order_tools")
	up := api.initUpload(ts.ID, 10)
	expectStatus(t, api.putBytes("/toolsetUploads/"+up.ID+"/chunks/0", []byte("aaaaaaaaaa")), http.StatusNoContent)
	expectStatus(t, api.putBytes("/toolsetUploads/"+up.ID+"/chunks/0", []byte("bbbbbbbbbb")), http.StatusNoContent)
	got := decodeData[ToolsetUpload](t, api.call(http.MethodGet, "/toolsetUploads/"+up.ID, nil))
	if got.ReceivedBytes != 10 || len(got.ReceivedChunks) != 1 {
		t.Errorf("after replacing chunk 0: %+v", got)
	}
}

func TestToolsetUploads_InitValidation(t *testing.T) {
	api := newToolsetAPI(t)
	ts := api.create("order_tools")
	body := func(m map[string]any) map[string]any {
		out := map[string]any{"toolsetId": ts.ID, "filename": "a.zip", "totalSize": 10}
		for k, v := range m {
			if v == nil {
				delete(out, k)
				continue
			}
			out[k] = v
		}
		return out
	}
	post := func(m map[string]any) *httptest.ResponseRecorder {
		return api.call(http.MethodPost, "/toolsetUploads", body(m))
	}
	expectDetail(t, post(map[string]any{"toolsetId": nil}), http.StatusUnprocessableEntity, "toolsetId")
	expectDetail(t, post(map[string]any{"toolsetId": "not-a-uuid"}), http.StatusUnprocessableEntity, "toolsetId")
	expectDetail(t, post(map[string]any{"filename": "tools.tar"}), http.StatusUnprocessableEntity, "filename")
	expectDetail(t, post(map[string]any{"totalSize": 0}), http.StatusUnprocessableEntity, "totalSize")
	expectDetail(t, post(map[string]any{"totalSize": 100<<20 + 1}), http.StatusUnprocessableEntity, "totalSize")
	expectStatus(t, post(map[string]any{"toolsetId": missingID}), http.StatusNotFound)
	expectDetail(t, post(map[string]any{"extra": 1}), http.StatusBadRequest, "extra")
	expectStatus(t, post(map[string]any{"filename": "TOOLS.ZIP"}), http.StatusCreated)

	rec := post(map[string]any{"toolsetId": "not-a-uuid", "filename": "tools.tar", "totalSize": -1})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	details := decodeError(t, rec).ValidationDetails
	for _, field := range []string{"toolsetId", "filename", "totalSize"} {
		if len(details[field]) == 0 {
			t.Errorf("validationDetails has nothing for %q, want every field's problem in one response: %v", field, details)
		}
	}
}

func TestToolsetUploads_ChunkBounds(t *testing.T) {
	api := newToolsetAPI(t)
	ts := api.create("order_tools")
	up := api.initUpload(ts.ID, 10)
	path := "/toolsetUploads/" + up.ID + "/chunks/"

	expectDetail(t, api.putBytes(path+"1", []byte("a")), http.StatusUnprocessableEntity, "id")
	rec := api.putBytes(path+"0", []byte(strings.Repeat("a", 11)))
	expectStatus(t, rec, http.StatusRequestEntityTooLarge)
	decodeError(t, rec)
	expectDetail(t, api.putBytes(path+"0", nil), http.StatusUnprocessableEntity, "body")
	expectDetail(t, api.putBytes(path+"0", []byte("abc")), http.StatusUnprocessableEntity, "body")
	expectDetail(t, api.putBytes(path+"-1", []byte("a")), http.StatusBadRequest, "id")
	expectDetail(t, api.putBytes(path+"x", []byte("a")), http.StatusBadRequest, "id")
	expectStatus(t, api.putBytes("/toolsetUploads/"+missingID+"/chunks/0", []byte("a")), http.StatusNotFound)
	expectDetail(t, api.putBytes("/toolsetUploads/not-a-uuid/chunks/0", []byte("a")), http.StatusBadRequest, "toolsetUploadId")
}

func TestToolsetUploads_Complete(t *testing.T) {
	api := newToolsetAPI(t)
	ts := api.create("order_tools")
	up := api.initUpload(ts.ID, 4<<20+3)
	expectDetail(t, api.call(http.MethodPut, "/toolsetUploads/"+up.ID+"/complete", nil), http.StatusUnprocessableEntity, "chunks")
	expectStatus(t, api.putBytes("/toolsetUploads/"+up.ID+"/chunks/1", []byte("abc")), http.StatusNoContent)
	expectDetail(t, api.call(http.MethodPut, "/toolsetUploads/"+up.ID+"/complete", nil), http.StatusUnprocessableEntity, "chunks")
	expectStatus(t, api.call(http.MethodPut, "/toolsetUploads/"+missingID+"/complete", nil), http.StatusNotFound)
	expectDetail(t, api.call(http.MethodPut, "/toolsetUploads/not-a-uuid/complete", nil), http.StatusBadRequest, "id")
}

func TestToolsetUploads_Scopes(t *testing.T) {
	api := newToolsetAPI(t)
	ts := api.create("order_tools")
	up := api.initUpload(ts.ID, 10)
	read := rbac.ScopeToolsetRead
	for _, rec := range []*httptest.ResponseRecorder{
		api.call(http.MethodPost, "/toolsetUploads", map[string]any{"toolsetId": ts.ID, "filename": "a.zip", "totalSize": 10}, read),
		api.putBytes("/toolsetUploads/"+up.ID+"/chunks/0", []byte("a"), read),
		api.call(http.MethodPut, "/toolsetUploads/"+up.ID+"/complete", nil, read),
		api.call(http.MethodGet, "/toolsetUploads/"+up.ID, nil, read),
	} {
		expectStatus(t, rec, http.StatusForbidden)
	}
	expectStatus(t, api.call(http.MethodGet, "/toolsetBuildTarget", nil, read), http.StatusOK)
	expectStatus(t, api.call(http.MethodGet, "/toolsetBuildTarget", nil, rbac.ScopeSkillRead), http.StatusForbidden)
}

func TestToolsetBuildTarget_FallsBackThenFollowsTheToolRuntime(t *testing.T) {
	api := newToolsetAPI(t)

	cold := api.call(http.MethodGet, "/toolsetBuildTarget", nil)
	expectStatus(t, cold, http.StatusOK)
	if got := decodeData[ToolsetBuildTarget](t, cold); got.Source != "fallback" || got.OS == "" || got.Arch == "" {
		t.Errorf("cold platform: %+v", got)
	}
	if v := rawKeys(t, cold)["pythonVersion"]; string(v) != "null" {
		t.Errorf("pythonVersion = %s on a cold platform, want null", v)
	}

	api.deps.STRRuntimeRegistry.Observe("linux", "amd64", "3.12")
	warm := decodeData[ToolsetBuildTarget](t, api.call(http.MethodGet, "/toolsetBuildTarget", nil))
	if warm.Source != "str_init" || warm.OS != "linux" || warm.Arch != "amd64" || warm.PythonVersion == nil || *warm.PythonVersion != "3.12" {
		t.Errorf("after an STR reported: %+v", warm)
	}

	api.deps.STRRuntimeRegistry.Observe("linux", "arm64", "3.12")
	rec := api.call(http.MethodGet, "/toolsetBuildTarget", nil)
	expectStatus(t, rec, http.StatusConflict)
	body := decodeError(t, rec)
	targets := body.ValidationDetails["targets"]
	if len(targets) != 2 {
		t.Fatalf("targets %v, want both runtimes", body.ValidationDetails)
	}
	joined := strings.Join(targets, " ")
	if !strings.Contains(joined, "amd64") || !strings.Contains(joined, "arm64") {
		t.Errorf("targets %v", targets)
	}
}
