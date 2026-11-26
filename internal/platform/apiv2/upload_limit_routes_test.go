// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"bytes"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

func newUploadLimitAPI(t *testing.T, maxUploadBytes int) *entrypointAPI {
	t.Helper()
	core, _ := newTestCoreWithConfig(t, map[string]any{"max_upload_bytes": maxUploadBytes})
	return &entrypointAPI{apiClient: newAPIClient(t, core)}
}

var uploadLimitBundleRoutes = map[string]string{
	"toolset bundle": "/toolsets/" + missingID + "/bundle",
	"skill bundle":   "/skills/" + missingID + "/bundle",
}

func TestUploads_GlobalCapRefusesWhitespacePaddingBeforeDecode(t *testing.T) {
	core, _ := newTestCoreWithConfig(t, map[string]any{"max_upload_bytes": testMaxUploadBytes})
	api := &apiClient{t: t, handler: auth.DevMiddleware()(core.Handler()), core: core}
	expectStatus(t, api.call(http.MethodPost, "/toolsets", `{"name":"under_cap"}`), http.StatusCreated)

	rec := api.call(http.MethodPost, "/toolsets", strings.Repeat(" ", testMaxUploadBytes)+`{"name":"over_cap"}`)
	expectStatus(t, rec, http.StatusRequestEntityTooLarge)
	if msg := decodeError(t, rec).Message; msg != msgBodyTooLarge {
		t.Errorf("message %q, want %q", msg, msgBodyTooLarge)
	}
}

func TestUploads_EveryBundleUploadHonoursMaxUploadBytes(t *testing.T) {
	api := newUploadLimitAPI(t, testMaxUploadBytes)
	for name, path := range uploadLimitBundleRoutes {
		t.Run(name, func(t *testing.T) {
			expectStatus(t, api.upload(http.MethodPut, path, "bundle.zip", []byte("x")), http.StatusNotFound)
			rec := api.upload(http.MethodPut, path, "bundle.zip", bytes.Repeat([]byte("x"), testMaxUploadBytes+1))
			expectStatus(t, rec, http.StatusRequestEntityTooLarge)
			if msg := decodeError(t, rec).Message; msg != msgBodyTooLarge {
				t.Errorf("message %q, want %q", msg, msgBodyTooLarge)
			}
		})
	}
}

func TestUploads_SpilledMultipartFilesAreRemoved(t *testing.T) {
	api := newUploadLimitAPI(t, 2*apicontract.MultipartMemory)
	spillDir := t.TempDir()
	t.Setenv("TMPDIR", spillDir)

	for name, path := range uploadLimitBundleRoutes {
		t.Run(name, func(t *testing.T) {
			expectStatus(t, api.upload(http.MethodPut, path, "bundle.zip", bytes.Repeat([]byte("x"), apicontract.MultipartMemory+1)), http.StatusNotFound)
			left, err := filepath.Glob(filepath.Join(spillDir, "multipart-*"))
			if err != nil {
				t.Fatalf("glob: %v", err)
			}
			if len(left) != 0 {
				t.Errorf("multipart temp files left behind: %v", left)
			}
		})
	}
}
