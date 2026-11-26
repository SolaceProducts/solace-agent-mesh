// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"
)

type unauthenticatedIsolatedToolsHandler struct {
	toolsetsDir string
	logger      *slog.Logger
}

func (w *Worker) IsolatedToolsHandler() http.Handler {
	cfg := w.cfg.Sandbox.Isolated
	if w.cfg.Sandbox.Mode != sandboxModeKubernetesJob || cfg.ToolsetsDir == "" {
		return nil
	}
	w.logger.Warn("serving tool files to isolated jobs without authentication",
		"path", IsolatedToolsURLPath, "toolsetsDir", cfg.ToolsetsDir)
	return &unauthenticatedIsolatedToolsHandler{toolsetsDir: cfg.ToolsetsDir, logger: w.logger}
}

func (h *unauthenticatedIsolatedToolsHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		rw.Header().Set("Allow", "GET, HEAD")
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rel := strings.TrimPrefix(req.URL.Path, IsolatedToolsURLPath)
	if !fs.ValidPath(rel) || rel == "." {
		http.Error(rw, "invalid path", http.StatusBadRequest)
		return
	}
	root, err := os.OpenRoot(h.toolsetsDir)
	if err != nil {
		h.logger.Error("isolated tool file request: toolsets dir unavailable", "error", err)
		http.Error(rw, "unavailable", http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = root.Close() }()

	file, err := root.Open(rel)
	if err != nil {
		http.NotFound(rw, req)
		return
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(rw, req)
		return
	}
	h.logger.Debug("serving isolated tool file", "path", rel, "size", info.Size())
	http.ServeContent(rw, req, info.Name(), info.ModTime(), file)
}
