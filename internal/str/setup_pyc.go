// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const pycMarkerFile = ".sam-pyc-done"

const precompileTimeout = 10 * time.Minute

func precompilePython(ctx context.Context, runner *SandboxRunner, toolDir string, logger *slog.Logger) {
	compile := func(ctx context.Context, interpreter, dir string, logger *slog.Logger) error {
		return runCompileAll(ctx, runner, interpreter, dir, logger)
	}
	precompilePythonWith(ctx, toolDir, resolveSTRPython, compile, logger)
}

type compileAllFunc func(ctx context.Context, interpreter, dir string, logger *slog.Logger) error

func precompilePythonWith(ctx context.Context, toolDir string, interpreter func() (path, version string), compile compileAllFunc, logger *slog.Logger) {
	if !hasPythonSources(toolDir) {
		return
	}
	pyPath, pyVersion := interpreter()
	if pyPath == "" {
		logger.Debug("python bytecode precompile skipped: no interpreter resolved", "dir", toolDir)
		return
	}
	stamp := pyPath + " " + pyVersion
	if data, err := readFileIn(toolDir, pycMarkerFile); err == nil && strings.TrimSpace(string(data)) == stamp {
		return
	}
	if err := probeWritable(toolDir); err != nil {
		if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EROFS) {
			logger.Debug("python bytecode precompile skipped: tool dir is read-only", "dir", toolDir, "error", err)
		} else {
			logger.Warn("python bytecode precompile skipped: tool dir not writable", "dir", toolDir, "error", err)
		}
		return
	}

	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, precompileTimeout)
	defer cancel()
	start := time.Now()
	if err := compile(ctx, pyPath, toolDir, logger); err != nil {
		switch {
		case parent.Err() != nil:
			logger.Debug("python bytecode precompile cancelled", "dir", toolDir)
			return
		case ctx.Err() != nil:
			logger.Warn("python bytecode precompile timed out; modules compile at import time instead",
				"dir", toolDir, "timeout", precompileTimeout)
			return
		}
		logger.Warn("python bytecode precompile finished with errors; affected modules compile at import time instead",
			"dir", toolDir, "error", err, "duration_ms", time.Since(start).Milliseconds())
		if !rejectedSomeModules(ctx, err) {
			return
		}
	} else {
		logger.Info("python bytecode precompiled", "dir", toolDir, "duration_ms", time.Since(start).Milliseconds())
	}
	if err := writeFileIn(toolDir, pycMarkerFile, []byte(stamp+"\n"), 0o600); err != nil {
		logger.Warn("failed to write bytecode precompile marker; every reload recompiles the whole tree",
			"dir", toolDir, "error", err)
	}
}

func rejectedSomeModules(ctx context.Context, err error) bool {
	var exitErr *exec.ExitError
	return ctx.Err() == nil && errors.As(err, &exitErr)
}

func probeWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".sam-pyc-probe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}

func hasPythonSources(toolDir string) bool {
	if hasPythonLayer(toolDir) {
		return true
	}
	entries, err := os.ReadDir(toolDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".py") {
			return true
		}
	}
	return false
}

func compileAllArgs(dir string) []string {
	workers := strconv.Itoa(runtime.GOMAXPROCS(0))
	return []string{"-I", "-m", "compileall", "-q", "-j", workers, dir}
}

func runCompileAll(ctx context.Context, runner *SandboxRunner, interpreter, dir string, logger *slog.Logger) error {
	logger.Debug("running compileall", "interpreter", interpreter, "dir", dir)
	if err := runner.runInstallStep(ctx, installStep{
		name: "compileall", toolDir: dir, executable: interpreter, args: compileAllArgs(dir),
		writableToolDir: true, stdout: os.Stderr, stderr: os.Stderr,
	}); err != nil {
		return fmt.Errorf("compileall %s: %w", dir, err)
	}
	return nil
}
