// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSetupWorkDir_CreatesScratchUnderInstanceDir(t *testing.T) {
	base := t.TempDir()
	runner := NewSandboxRunner(SandboxConfig{WorkBaseDir: base}, nil)
	t.Cleanup(runner.releaseInstanceDir)

	workDir, err := runner.setupWorkDir()
	if err != nil {
		t.Fatalf("setupWorkDir: %v", err)
	}

	instanceDir, err := runner.instanceWorkDir()
	if err != nil {
		t.Fatalf("instanceWorkDir: %v", err)
	}
	if filepath.Dir(instanceDir) != base || !isInstanceDirName(filepath.Base(instanceDir)) {
		t.Fatalf("instance dir = %q, want %s<digits> directly under %q", instanceDir, instanceDirPrefix, base)
	}
	if filepath.Dir(workDir) != instanceDir || !strings.HasPrefix(filepath.Base(workDir), workDirPrefix) {
		t.Fatalf("work dir = %q, want %s<random> inside instance dir %q", workDir, workDirPrefix, instanceDir)
	}
	for _, sub := range []string{"input", "output", workDirHome, workDirTmp} {
		if info, err := os.Stat(filepath.Join(workDir, sub)); err != nil || !info.IsDir() {
			t.Errorf("work dir is missing %s/: %v", sub, err)
		}
	}
}

func makeInstanceDir(t *testing.T, base, name string, withLock bool, mtime time.Time) string {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(filepath.Join(dir, workDirPrefix+"123", workDirTmp), 0o700); err != nil {
		t.Fatal(err)
	}
	if withLock {
		lockPath := filepath.Join(dir, instanceLockName)
		if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(lockPath, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(dir, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return dir
}

func holdLock(t *testing.T, dir string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, instanceLockName), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	if err := tryLockFile(f); err != nil {
		t.Fatalf("lock %s: %v", filepath.Base(dir), err)
	}
}

func TestSweepStaleInstanceDirs(t *testing.T) {
	base := t.TempDir()
	old := time.Now().Add(-2 * staleInstanceGrace)

	dead := makeInstanceDir(t, base, instanceDirPrefix+"100", true, old)
	noLock := makeInstanceDir(t, base, instanceDirPrefix+"200", false, old)
	justCreated := makeInstanceDir(t, base, instanceDirPrefix+"300", true, time.Now())
	live := makeInstanceDir(t, base, instanceDirPrefix+"400", true, old)
	notOurs := makeInstanceDir(t, base, instanceDirPrefix+"logs", true, old)
	legacy := makeInstanceDir(t, base, "task-legacy-456", true, old)
	holdLock(t, live)
	diedMidCall := makeInstanceDir(t, base, instanceDirPrefix+"500", true, old)
	if err := os.Chtimes(diedMidCall, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}

	runner := NewSandboxRunner(SandboxConfig{WorkBaseDir: base}, slog.Default())
	t.Cleanup(runner.releaseInstanceDir)
	runner.sweepStaleInstanceDirs(t.Context())
	own, err := runner.instanceWorkDir()
	if err != nil {
		t.Fatalf("instanceWorkDir: %v", err)
	}

	for _, gone := range []string{dead, diedMidCall} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("dead instance %s survived the sweep (stat err = %v)", filepath.Base(gone), err)
		}
	}
	for _, kept := range []string{noLock, justCreated, live, notOurs, legacy, own} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s was swept: %v", filepath.Base(kept), err)
		}
	}
}

func TestSweepStaleInstanceDirs_StopsWhenCancelled(t *testing.T) {
	base := t.TempDir()
	dead := makeInstanceDir(t, base, instanceDirPrefix+"100", true, time.Now().Add(-2*staleInstanceGrace))
	runner := NewSandboxRunner(SandboxConfig{WorkBaseDir: base}, nil)
	t.Cleanup(runner.releaseInstanceDir)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	runner.sweepStaleInstanceDirs(ctx)

	if _, err := os.Stat(dead); err != nil {
		t.Errorf("cancelled sweep still removed a dir: %v", err)
	}
}

func TestInstanceWorkDir_RetriesAfterFailedClaim(t *testing.T) {
	base := filepath.Join(t.TempDir(), "work")
	if err := os.WriteFile(base, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := NewSandboxRunner(SandboxConfig{WorkBaseDir: base}, nil)
	t.Cleanup(runner.releaseInstanceDir)

	if _, err := runner.setupWorkDir(); err == nil {
		t.Fatal("setupWorkDir succeeded with the base dir path taken by a file")
	}
	if err := os.Remove(base); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.setupWorkDir(); err != nil {
		t.Fatalf("setupWorkDir after the cause was fixed: %v", err)
	}
}

func TestInstanceWorkDir_RestoresRemovedLock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows refuses to delete a file another handle holds open")
	}
	base := t.TempDir()
	runner := NewSandboxRunner(SandboxConfig{WorkBaseDir: base}, nil)
	t.Cleanup(runner.releaseInstanceDir)
	instanceDir, err := runner.instanceWorkDir()
	if err != nil {
		t.Fatal(err)
	}

	for _, remove := range []string{filepath.Join(instanceDir, instanceLockName), instanceDir} {
		if err := os.RemoveAll(remove); err != nil {
			t.Fatal(err)
		}
		if _, err := runner.setupWorkDir(); err != nil {
			t.Fatalf("setupWorkDir after removing %s: %v", filepath.Base(remove), err)
		}
		lock, err := os.OpenFile(filepath.Join(instanceDir, instanceLockName), os.O_RDWR, 0)
		if err != nil {
			t.Fatalf("lock file not restored after removing %s: %v", filepath.Base(remove), err)
		}
		lockErr := tryLockFile(lock)
		_ = lock.Close()
		if lockErr == nil {
			t.Fatalf("restored lock file is not held after removing %s", filepath.Base(remove))
		}
	}
}

func TestReleaseInstanceDir_RemovesEmptyInstanceDir(t *testing.T) {
	base := t.TempDir()
	runner := NewSandboxRunner(SandboxConfig{WorkBaseDir: base}, nil)
	workDir, err := runner.setupWorkDir()
	if err != nil {
		t.Fatalf("setupWorkDir: %v", err)
	}
	runner.scheduleWorkDirCleanup(workDir)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	runner.runWorkDirCleanup(ctx)
	runner.releaseInstanceDir()

	leftover, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(leftover) != 0 {
		t.Fatalf("base dir not empty after release: %v", leftover)
	}
}

func TestReleaseInstanceDir_LeftoverWorkDirIsSweptByNextSTR(t *testing.T) {
	base := t.TempDir()
	first := NewSandboxRunner(SandboxConfig{WorkBaseDir: base}, nil)
	workDir, err := first.setupWorkDir()
	if err != nil {
		t.Fatalf("setupWorkDir: %v", err)
	}
	instanceDir := filepath.Dir(workDir)
	first.releaseInstanceDir()

	lockPath := filepath.Join(instanceDir, instanceLockName)
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file removed while a work dir was left behind: %v", err)
	}
	old := time.Now().Add(-2 * staleInstanceGrace)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}

	next := NewSandboxRunner(SandboxConfig{WorkBaseDir: base}, nil)
	t.Cleanup(next.releaseInstanceDir)
	next.sweepStaleInstanceDirs(t.Context())

	if _, err := os.Stat(instanceDir); !os.IsNotExist(err) {
		t.Errorf("released instance dir with a leftover work dir survived the next sweep (stat err = %v)", err)
	}
}

func TestSweepStaleInstanceDirs_RemovesReadOnlyTree(t *testing.T) {
	skipOnWindows(t)
	base := t.TempDir()
	dead := makeInstanceDir(t, base, instanceDirPrefix+"100", true, time.Now().Add(-2*staleInstanceGrace))
	readOnly := filepath.Join(dead, workDirPrefix+"123", workDirHome, "ro")
	if err := os.MkdirAll(readOnly, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(readOnly, "f"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(readOnly, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o700) })

	runner := NewSandboxRunner(SandboxConfig{WorkBaseDir: base}, nil)
	t.Cleanup(runner.releaseInstanceDir)
	runner.sweepStaleInstanceDirs(t.Context())

	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Errorf("dead instance with a read-only subtree survived the sweep (stat err = %v)", err)
	}
}

func TestWorkDirTmp_LeavesRoomForPythonSockets(t *testing.T) {
	const pythonListenerSuffix = "/pymp-xxxxxxxx/listener-xxxxxxxx"
	const longestMkdirTempSuffix = "4294967295"
	var cfg SandboxConfig
	cfg.setDefaults()

	tmp := filepath.Join(cfg.WorkBaseDir, instanceDirPrefix+longestMkdirTempSuffix, workDirPrefix+longestMkdirTempSuffix, workDirTmp)
	if got := len(tmp) + len(pythonListenerSuffix); got >= maxUnixSocketPath {
		t.Errorf("worst-case listener path is %d bytes (TMPDIR %q), want under %d", got, tmp, maxUnixSocketPath)
	}
}

func TestWorker_StartSweepsAndStopReleases(t *testing.T) {
	base := t.TempDir()
	dead := makeInstanceDir(t, base, instanceDirPrefix+"100", true, time.Now().Add(-2*staleInstanceGrace))
	w := NewWorker(&testServices{brk: newTestBroker(t)}, WorkerConfig{
		WorkerID:      "sweep-worker",
		Namespace:     "test-ns",
		ManifestPaths: []string{writeTestManifest(t)},
		Sandbox:       SandboxConfig{Mode: "direct", WorkBaseDir: base},
	})
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForRemoval(t, dead)
	if err := w.Stop(t.Context()); err != nil {
		t.Fatalf("stop: %v", err)
	}

	leftover, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(leftover) != 0 {
		t.Errorf("base dir not empty after Stop: %v", leftover)
	}
}

func waitForRemoval(t *testing.T, dir string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s was not removed within the deadline", filepath.Base(dir))
}

func bwrapEnv(args []string) map[string]string {
	env := map[string]string{}
	for i := 0; i+2 < len(args); i++ {
		if args[i] == bwrapSetenv {
			env[args[i+1]] = args[i+2]
		}
	}
	return env
}

func TestBuildBwrapCommand_ScratchHomeAndTmpOnWorkDir(t *testing.T) {
	runner := &SandboxRunner{cfg: SandboxConfig{Mode: "bwrap"}, logger: slog.Default()}
	runner.cfg.setDefaults()
	workDir := "/tmp/sam-str-work/str-123/w-456"

	cmd, err := runner.buildBwrapCommand(t.Context(), &ManifestEntry{ToolName: "t"}, []string{"/tmp/args.json"}, workDir, nil, "", slog.Default())
	if err != nil {
		t.Fatalf("buildBwrapCommand: %v", err)
	}
	env := bwrapEnv(cmd.Args)
	if got, want := env["HOME"], filepath.Join(workDir, workDirHome); got != want {
		t.Errorf("HOME = %q, want %q", got, want)
	}
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		if got, want := env[key], filepath.Join(workDir, workDirTmp); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func mountIndex(args []string, flag, dest string) int {
	for i := 0; i+1 < len(args); i++ {
		if args[i] != flag {
			continue
		}
		if flag == bwrapFlagTmpfs && args[i+1] == dest {
			return i
		}
		if flag == bwrapFlagBind && i+2 < len(args) && args[i+2] == dest {
			return i
		}
	}
	return -1
}

func TestBuildBwrapCommand_HidesOtherCallsWorkDirs(t *testing.T) {
	cases := []struct {
		name, base, profile string
		wantBaseTmpfs       bool
	}{
		{"default base under the /tmp tmpfs", "/tmp/sam-str-work", "standard", false},
		{"base outside /tmp", "/data/sam-str-work", "standard", true},
		{"base under /var with a writable /var", "/var/lib/sam-str-work", "permissive", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &SandboxRunner{cfg: SandboxConfig{Mode: "bwrap", WorkBaseDir: tc.base}, logger: slog.Default()}
			runner.cfg.setDefaults()
			workDir := filepath.Join(tc.base, "str-123", "w-456")

			cmd, err := runner.buildBwrapCommand(t.Context(),
				&ManifestEntry{ToolName: "t", SandboxProfile: tc.profile}, []string{"/tmp/args.json"}, workDir, nil, "", slog.Default())
			if err != nil {
				t.Fatalf("buildBwrapCommand: %v", err)
			}
			bind := mountIndex(cmd.Args, bwrapFlagBind, workDir)
			if bind < 0 {
				t.Fatalf("work dir is not bound: %v", cmd.Args)
			}
			baseTmpfs := mountIndex(cmd.Args, bwrapFlagTmpfs, tc.base)
			if tc.wantBaseTmpfs && (baseTmpfs < 0 || baseTmpfs > bind) {
				t.Errorf("want --tmpfs %s before the work dir bind: %v", tc.base, cmd.Args)
			}
			if !tc.wantBaseTmpfs && baseTmpfs >= 0 {
				t.Errorf("unexpected --tmpfs %s: %v", tc.base, cmd.Args)
			}
			for _, hiding := range []string{"/tmp", "/var"} {
				if i := mountIndex(cmd.Args, bwrapFlagTmpfs, hiding); i > bind {
					t.Errorf("--tmpfs %s comes after the work dir bind and hides it: %v", hiding, cmd.Args)
				}
			}
		})
	}
}

func TestExecute_DirectMode_TmpdirIsPerCallAndRemoved(t *testing.T) {
	skipOnWindows(t)
	t.Setenv("HOME", "/home/someone")
	t.Setenv("TMPDIR", t.TempDir())
	script := writeScript(t, t.TempDir(), "env.sh",
		`printf '{"result":{"tmpdir":"%s","tmp":"%s","temp":"%s","home":"%s"}}' "$TMPDIR" "$TMP" "$TEMP" "$HOME" > result.json`)
	runner := NewSandboxRunner(SandboxConfig{
		Mode:                    "direct",
		WorkBaseDir:             t.TempDir(),
		DefaultTimeoutSeconds:   10,
		MaxConcurrentExecutions: 2,
	}, slog.Default())
	t.Cleanup(runner.releaseInstanceDir)

	res, invokeErr := runner.Execute(t.Context(), minimalParams(), minimalEntry(script), nil, func(string) {})
	if invokeErr != nil {
		t.Fatalf("Execute: %s %s", invokeErr.Code, invokeErr.Message)
	}

	instanceDir, err := runner.instanceWorkDir()
	if err != nil {
		t.Fatalf("instanceWorkDir: %v", err)
	}
	tmpdir, _ := res.ToolResult["tmpdir"].(string)
	if filepath.Base(tmpdir) != workDirTmp || filepath.Dir(filepath.Dir(tmpdir)) != instanceDir {
		t.Fatalf("TMPDIR = %q, want <work dir>/%s inside %q", tmpdir, workDirTmp, instanceDir)
	}
	for _, key := range []string{"tmp", "temp"} {
		if res.ToolResult[key] != tmpdir {
			t.Errorf("%s = %v, want %q", strings.ToUpper(key), res.ToolResult[key], tmpdir)
		}
	}
	if res.ToolResult["home"] != "/home/someone" {
		t.Errorf("HOME = %v, want the STR's own HOME in direct mode", res.ToolResult["home"])
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	runner.runWorkDirCleanup(ctx)
	if _, err := os.Stat(filepath.Dir(tmpdir)); !os.IsNotExist(err) {
		t.Errorf("work dir survived cleanup (stat err = %v)", err)
	}
}

func TestIsInstanceDirName(t *testing.T) {
	for name, want := range map[string]bool{
		"str-1234567890": true,
		"str-":           false,
		"str-logs":       false,
		"str-12a":        false,
		"task-1-123":     false,
	} {
		if got := isInstanceDirName(name); got != want {
			t.Errorf("isInstanceDirName(%q) = %v, want %v", name, got, want)
		}
	}
}
