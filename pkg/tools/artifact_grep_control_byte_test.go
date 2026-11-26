// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestGrepSearchesTextFileWithAStrayControlByte(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	ctx := context.Background()

	saveTestArtifact(ctx, svc, toolCtx, "serial.log",
		"boot ok\x00\nservice failed: ERROR 500\ndone\n", "text/plain")

	gt := &GrepTool{}
	result, err := gt.Execute(ctx, map[string]any{"pattern": "ERROR", "filename": "serial.log"}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Message, "No matches found") {
		t.Errorf("one NUL byte discarded a searchable log: %q", result.Message)
	}
	if names, _ := result.Data["binary_files"].([]string); len(names) != 0 {
		t.Errorf("file was searched and matched, so it must not also be called binary: %v", names)
	}
}

func TestGrepFlagsUnrecognisedBinaryOnlyWhenItFindsNothing(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	ctx := context.Background()

	saveTestArtifact(ctx, svc, toolCtx, "blob.bin",
		"\x00\x01\x02\x03\x04 nothing textual", "application/octet-stream")

	gt := &GrepTool{}
	result, err := gt.Execute(ctx, map[string]any{"pattern": "ERROR", "filename": "blob.bin"}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q", result.Status)
	}
	names, _ := result.Data["binary_files"].([]string)
	if len(names) != 1 || names[0] != "blob.bin" {
		t.Errorf("an empty result on an unreadable file must say so; binary_files = %v",
			result.Data["binary_files"])
	}
}
