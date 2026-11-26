// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"bytes"
	"net/http"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const testMaxUploadBytes = 4 << 10

func TestUploads_MultipartHonoursMaxUploadBytes(t *testing.T) {
	core, _ := newTestCoreWithConfig(t, map[string]any{"max_upload_bytes": testMaxUploadBytes})
	api := &entrypointAPI{apiClient: newAPIClient(t, core)}

	expectStatus(t, uploadForm(api, http.MethodPost, "/skills", "order-lookup.zip", skillZip(t, "order-lookup"), nil), http.StatusCreated)

	rec := uploadForm(api, http.MethodPost, "/skills", "big.zip", bytes.Repeat([]byte("x"), 2*testMaxUploadBytes), nil)
	expectStatus(t, rec, http.StatusRequestEntityTooLarge)
	if msg := decodeError(t, rec).Message; msg != msgBodyTooLarge {
		t.Errorf("message %q, want %q", msg, msgBodyTooLarge)
	}
}

func TestToolsetUploads_TotalSizeHonoursMaxUploadBytes(t *testing.T) {
	t.Run("raised", func(t *testing.T) {
		api := newToolsetAPIWithConfig(t, map[string]any{"max_upload_bytes": 300 << 20})
		ts := api.create("order_tools")
		if up := api.initUpload(ts.ID, 150<<20); up.TotalSize != 150<<20 {
			t.Errorf("totalSize %d, want %d", up.TotalSize, 150<<20)
		}
	})
	t.Run("lowered", func(t *testing.T) {
		api := newToolsetAPIWithConfig(t, map[string]any{"max_upload_bytes": testMaxUploadBytes})
		ts := api.create("order_tools")
		rec := api.call(http.MethodPost, "/toolsetUploads", map[string]any{"toolsetId": ts.ID, "filename": "order_tools.zip", "totalSize": testMaxUploadBytes + 1})
		expectDetail(t, rec, http.StatusUnprocessableEntity, "totalSize")
	})
}

func TestToolsetUploads_ChunkCapIsIndependentOfMaxUploadBytes(t *testing.T) {
	api := newToolsetAPI(t)
	ts := api.create("order_tools")
	up := api.initUpload(ts.ID, 4*entplatform.MaxChunkBytes)

	rec := api.putBytes("/toolsetUploads/"+up.ID+"/chunks/0", make([]byte, entplatform.MaxChunkBytes+1))
	expectStatus(t, rec, http.StatusRequestEntityTooLarge)
	if msg := decodeError(t, rec).Message; msg != msgBodyTooLarge {
		t.Errorf("message %q, want %q", msg, msgBodyTooLarge)
	}
}
