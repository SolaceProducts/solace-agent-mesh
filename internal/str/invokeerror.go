// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	ToolOriginBuiltin = "builtin"
	ToolOriginCustom  = "custom"
)

func newInvokeError(code remotetool.ErrorCode, format string, args ...any) *remotetool.InvokeError {
	return &remotetool.InvokeError{
		Code:    code,
		Message: fmt.Sprintf(format, args...),
		Site:    tool.CallerSite(1),
	}
}

func (e *ManifestEntry) isOurs() bool {
	return e != nil && (e.IsBuiltin || e.inProcess != nil || e.Origin == ToolOriginBuiltin)
}

func (e *ManifestEntry) toolCategory() string {
	if e.isOurs() {
		return taxonomy.ToolCategoryBuiltin
	}
	return taxonomy.ToolCategoryCustom
}

func logCapturedStderr(log *slog.Logger, ours bool, redactedStderr string, dropped int) {
	if redactedStderr == "" {
		return
	}
	attrs := []any{"bytes", len(redactedStderr), "droppedBytes", dropped}
	if ours {
		attrs = append(attrs, "stderrTail", stderrTail(redactedStderr))
	}
	log.Debug("tool stderr captured", attrs...)
}

func dirLogAttrs(ours bool, path string, key ...string) []any {
	if !ours {
		return nil
	}
	name := "dir"
	if len(key) > 0 {
		name = key[0]
	}
	return []any{name, path}
}

func outputArtifactFailureAttrs(file string, err error) []any {
	return append(userDataFailureAttrs(err), "fileNameLen", len(file))
}

func userDataFailureAttrs(err error) []any {
	return []any{"errorClass", tool.ClassifyCause(err), "errorLen", len(err.Error())}
}

func failureLogAttrs(ours bool, err error) []any {
	if ours {
		return []any{"error", err}
	}
	return userDataFailureAttrs(err)
}
