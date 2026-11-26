// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const syntaxCheckTimeout = 10 * time.Second

const pythonParse = "import ast,sys; ast.parse(open(sys.argv[1], encoding='utf-8').read(), sys.argv[1])"

func (w *workspace) syntaxWarning(ctx context.Context, path string) string {
	abs, err := w.resolve(path)
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return ""
	}
	if problem := syntaxProblem(ctx, abs, data, w.env, !w.noExec); problem != "" {
		return fmt.Sprintf("\nwarning: %s no longer parses: %s\nFix it before moving on.", w.rel(abs), problem)
	}
	return ""
}

func syntaxProblem(ctx context.Context, abs string, data []byte, env []string, runPython bool) string {
	switch strings.ToLower(filepath.Ext(abs)) {
	case ".go":
		if _, err := parser.ParseFile(token.NewFileSet(), filepath.Base(abs), data, parser.AllErrors); err != nil {
			return err.Error()
		}
	case ".json":
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			return err.Error()
		}
	case ".yaml", ".yml":
		if strings.Contains(string(data), "{{") {
			return ""
		}
		var v any
		if err := yaml.Unmarshal(data, &v); err != nil {
			return err.Error()
		}
	case ".py":
		if !runPython {
			return ""
		}
		python, err := exec.LookPath("python3")
		if err != nil {
			return ""
		}
		checkCtx, cancel := context.WithTimeout(ctx, syntaxCheckTimeout)
		defer cancel()
		cmd := execCommand(checkCtx, python, "-c", pythonParse, abs)
		if env != nil {
			cmd.Env = env
		}
		if out, err := cmd.CombinedOutput(); err != nil && checkCtx.Err() == nil {
			return lastLines(string(out), 4)
		}
	}
	return ""
}
