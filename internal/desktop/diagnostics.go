// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package desktop

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
)

const (
	diagnosticsDirName = "diagnostics"
	crashFilesRetained = 5
	ownerOnlyDirMode   = 0o700
)

type diagnosticsPaths struct {
	root  string
	logs  string
	crash string
}

func resolveDiagnosticsPaths() (diagnosticsPaths, error) {
	home, err := settings.ResolveHomeDir()
	if err != nil {
		return diagnosticsPaths{}, fmt.Errorf("resolve diagnostics dir: %w", err)
	}
	return prepareDiagnosticsPaths(filepath.Join(home, diagnosticsDirName))
}

func diagnosticsPathsOrResolve(cached diagnosticsPaths) (diagnosticsPaths, error) {
	if cached.root != "" {
		return cached, nil
	}
	return resolveDiagnosticsPaths()
}

func prepareDiagnosticsPaths(root string) (diagnosticsPaths, error) {
	p := diagnosticsPaths{
		root:  root,
		logs:  filepath.Join(root, "logs"),
		crash: filepath.Join(root, "crash"),
	}
	for _, dir := range []string{p.logs, p.crash} {
		if err := os.MkdirAll(dir, ownerOnlyDirMode); err != nil {
			return diagnosticsPaths{}, fmt.Errorf("create diagnostics dir %s: %w", dir, err)
		}
	}
	return p, nil
}

func (p diagnosticsPaths) desktopLog() string { return filepath.Join(p.logs, "desktop.log") }

func newDesktopLogHandler(w io.Writer) slog.Handler {
	return logging.NewHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}, "text")
}

type runIdentity struct {
	RunID     string
	StartedAt time.Time
	PID       int
	Version   string
	OS        string
	Arch      string
}

func newRunIdentity() runIdentity {
	return runIdentity{
		RunID:     idgen.New(),
		StartedAt: time.Now().UTC(),
		PID:       os.Getpid(),
		Version:   samversion.Version,
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}

func crashHeader(id runIdentity) string {
	var b strings.Builder
	fmt.Fprintf(&b, "=== SAM Desktop crash report ===\n")
	fmt.Fprintf(&b, "runId:     %s\n", id.RunID)
	fmt.Fprintf(&b, "version:   %s\n", id.Version)
	fmt.Fprintf(&b, "platform:  %s/%s\n", id.OS, id.Arch)
	fmt.Fprintf(&b, "goVersion: %s\n", runtime.Version())
	fmt.Fprintf(&b, "pid:       %d\n", id.PID)
	fmt.Fprintf(&b, "startedAt: %s\n", id.StartedAt.Format(time.RFC3339Nano))
	fmt.Fprintf(&b, "================================\n")
	return b.String()
}

type crashSink struct {
	path        string
	headerBytes int64
}

var setCrashOutput = debug.SetCrashOutput

func installCrashSink(paths diagnosticsPaths, id runIdentity, now time.Time) (_ crashSink, err error) {
	name := fmt.Sprintf("sam-crash-%s.log", now.UTC().Format("20060102-150405"))
	path := filepath.Join(paths.crash, name)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return crashSink{}, fmt.Errorf("open crash file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	defer func() { _ = f.Close() }()

	header := crashHeader(id)
	if _, err := f.WriteString(header); err != nil {
		return crashSink{}, fmt.Errorf("write crash header: %w", err)
	}
	if err := setCrashOutput(f, debug.CrashOptions{}); err != nil {
		return crashSink{}, fmt.Errorf("register crash output: %w", err)
	}
	return crashSink{path: path, headerBytes: int64(len(header))}, nil
}

func (c crashSink) release() error {
	if c.path == "" {
		return nil
	}
	if err := setCrashOutput(nil, debug.CrashOptions{}); err != nil {
		return fmt.Errorf("deregister crash output: %w", err)
	}
	if err := os.Remove(c.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove crash file %s: %w", c.path, err)
	}
	return nil
}

func pruneCrashFiles(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read crash dir %s: %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "sam-crash-") && strings.HasSuffix(e.Name(), ".log") {
			names = append(names, e.Name())
		}
	}
	if len(names) <= keep {
		return nil
	}
	slices.Sort(names)
	for _, name := range names[:len(names)-keep] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("prune crash file %s: %w", name, err)
		}
	}
	return nil
}
