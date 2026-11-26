// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type HostTool struct {
	Spec   sdk.ChatTool
	Handle func(ctx context.Context, arguments string) (string, error)
}

const (
	hostOutputMaxBytes = 8 << 10
	hostPreviewBytes   = 3 << 10
	hostOutputDir      = "host-output"
)

func checkHostTools(tools []HostTool) error {
	reserved := map[string]bool{"fetch": true, submitResultTool: true}
	for _, t := range innerToolSpecs {
		reserved[t.Name] = true
	}
	for _, t := range tools {
		switch {
		case t.Spec.Name == "" || t.Handle == nil:
			return fmt.Errorf("host tool %q needs a name and a handler", t.Spec.Name)
		case reserved[t.Spec.Name]:
			return fmt.Errorf("host tool %q is the name of another tool", t.Spec.Name)
		}
		reserved[t.Spec.Name] = true
	}
	return nil
}

func (w *workspace) hostTool(name string) (HostTool, bool) {
	for _, t := range w.hostTools {
		if t.Spec.Name == name {
			return t, true
		}
	}
	return HostTool{}, false
}

func (w *workspace) hostSpecs() []sdk.ChatTool {
	specs := make([]sdk.ChatTool, 0, len(w.hostTools))
	for _, t := range w.hostTools {
		specs = append(specs, t.Spec)
	}
	return specs
}

func (w *workspace) runHost(ctx context.Context, t HostTool, arguments string) (string, error) {
	out, err := t.Handle(ctx, arguments)
	if err != nil || len(out) <= hostOutputMaxBytes {
		return out, err
	}
	w.hostSeq++
	rel := filepath.Join(stateDirName, hostOutputDir, fmt.Sprintf("%s-%d.txt", t.Spec.Name, w.hostSeq))
	abs := filepath.Join(w.root, rel)
	if stErr := ensureStateDir(w.root); stErr != nil {
		return headTail(out, hostPreviewBytes, hostPreviewBytes), nil
	}
	if mkErr := os.MkdirAll(filepath.Dir(abs), 0o700); mkErr != nil {
		return headTail(out, hostPreviewBytes, hostPreviewBytes), nil
	}
	if wErr := os.WriteFile(abs, []byte(out), 0o600); wErr != nil {
		return headTail(out, hostPreviewBytes, hostPreviewBytes), nil
	}
	return fmt.Sprintf("%s\n…\n(output is %d bytes; all of it is in %s: grep it or read it in parts)",
		strings.ToValidUTF8(out[:hostPreviewBytes], ""), len(out), filepath.ToSlash(rel)), nil
}

func (w *workspace) refuseHostOutput(abs, path string) error {
	if within(filepath.Join(stateDir(w.root), hostOutputDir), abs) {
		return fmt.Errorf("%s is saved host tool output: read or grep it, but do not change it", path)
	}
	return nil
}
