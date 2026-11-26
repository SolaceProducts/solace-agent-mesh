// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"bytes"
	"crypto/rand"
	"net/http"
	"sync"
	"testing"
)

func toolsetUploadsSharedAPIs(t *testing.T, n int) []*toolsetAPI {
	t.Helper()
	shared := map[string]any{
		"database_url": testTemplate.Clone(t),
		"str_store":    map[string]any{"type": "filesystem", "base_path": t.TempDir()},
	}
	apis := make([]*toolsetAPI, n)
	for i := range apis {
		apis[i] = newToolsetAPIWithConfig(t, shared)
	}
	return apis
}

func (a *toolsetAPI) toolsetUploadsComplete(uploadID string) int {
	return a.call(http.MethodPut, "/toolsetUploads/"+uploadID+"/complete", nil).Code
}

func TestToolsetUploads_StepsReachDifferentProcesses(t *testing.T) {
	apis := toolsetUploadsSharedAPIs(t, 2)
	a, b := apis[0], apis[1]
	ts := a.create("two_gateway_tools")
	incompressible := make([]byte, 5<<20)
	_, _ = rand.Read(incompressible)
	bundle := zipOf(t, map[string]string{
		"two_gateway_tools/manifest.yaml": "name: two_gateway_tools\nversion: \"1.0\"\n",
		"two_gateway_tools/data.bin":      string(incompressible),
	})

	up := a.initUpload(ts.ID, len(bundle))
	if up.ChunkSize >= int64(len(bundle)) || 2*up.ChunkSize < int64(len(bundle)) {
		t.Fatalf("chunkSize %d does not split the %d-byte bundle into two chunks", up.ChunkSize, len(bundle))
	}
	expectStatus(t, b.putBytes("/toolsetUploads/"+up.ID+"/chunks/0", bundle[:up.ChunkSize]), http.StatusNoContent)
	expectStatus(t, a.putBytes("/toolsetUploads/"+up.ID+"/chunks/1", bundle[up.ChunkSize:]), http.StatusNoContent)
	expectStatus(t, b.call(http.MethodPut, "/toolsetUploads/"+up.ID+"/complete", nil), http.StatusOK)

	download := a.call(http.MethodGet, "/toolsets/"+ts.ID+"/bundle", nil)
	expectStatus(t, download, http.StatusOK)
	if !bytes.Equal(download.Body.Bytes(), bundle) {
		t.Errorf("stored bundle differs from the uploaded one (%d of %d bytes)", download.Body.Len(), len(bundle))
	}
	if code := a.toolsetUploadsComplete(up.ID); code != http.StatusNotFound {
		t.Errorf("complete again on the other process: %d, want 404 once the upload is deleted", code)
	}
}

func TestToolsetUploads_AFailedCompleteDeletesTheUpload(t *testing.T) {
	api := newToolsetAPI(t)
	ts := api.create("cleanup_tools")
	notZip := []byte("not a zip file at all")
	up := api.initUpload(ts.ID, len(notZip))
	expectStatus(t, api.putBytes("/toolsetUploads/"+up.ID+"/chunks/0", notZip), http.StatusNoContent)

	if code := api.toolsetUploadsComplete(up.ID); code == http.StatusOK {
		t.Fatal("completing a bundle that is not a ZIP succeeded")
	}
	if code := api.toolsetUploadsComplete(up.ID); code != http.StatusNotFound {
		t.Errorf("second complete: %d, want 404 once the failed upload is deleted", code)
	}
}

func TestToolsetUploads_ConcurrentCompletesLeaveNoUploadBehind(t *testing.T) {
	api := newToolsetAPI(t)
	ts := api.create("concurrent_tools")
	bundle := zipOf(t, map[string]string{
		"concurrent_tools/manifest.yaml": "name: concurrent_tools\nversion: \"1.0\"\n",
		"concurrent_tools/tool.py":       "x=1",
	})
	up := api.initUpload(ts.ID, len(bundle))
	expectStatus(t, api.putBytes("/toolsetUploads/"+up.ID+"/chunks/0", bundle), http.StatusNoContent)

	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Go(func() { codes[i] = api.toolsetUploadsComplete(up.ID) })
	}
	wg.Wait()

	oks := 0
	for _, code := range codes {
		switch code {
		case http.StatusOK:
			oks++
		case http.StatusNotFound:
		default:
			t.Errorf("two concurrent completes answered %v, want each 200 or 404", codes)
		}
	}
	if oks == 0 {
		t.Errorf("two concurrent completes answered %v, want at least one 200", codes)
	}
	download := api.call(http.MethodGet, "/toolsets/"+ts.ID+"/bundle", nil)
	expectStatus(t, download, http.StatusOK)
	if !bytes.Equal(download.Body.Bytes(), bundle) {
		t.Errorf("stored bundle differs from the uploaded one (%d of %d bytes)", download.Body.Len(), len(bundle))
	}
	if code := api.toolsetUploadsComplete(up.ID); code != http.StatusNotFound {
		t.Errorf("complete after two concurrent ones: %d, want 404", code)
	}
}
