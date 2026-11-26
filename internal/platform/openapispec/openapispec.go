// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapispec

import (
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
)

const Path = "/api/v2/openapi.yaml"

const FileName = "sam-openapi.yaml"

const (
	specDir         = "spec"
	contentType     = "application/yaml"
	missingSpecText = "the OpenAPI description was not generated for this build (build with make generate)"
)

//go:embed all:spec
var embedded embed.FS

func Available() bool {
	return available(embedded)
}

func available(files fs.FS) bool {
	_, err := fs.Stat(files, specDir+"/"+FileName)
	return err == nil
}

func Handler(logger *slog.Logger) http.HandlerFunc {
	return serve(embedded, logger)
}

func serve(files fs.FS, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		data, err := fs.ReadFile(files, specDir+"/"+FileName)
		if err != nil {
			logger.Debug("OpenAPI description unavailable", "error", err)
			http.Error(w, missingSpecText, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(data)
	}
}
