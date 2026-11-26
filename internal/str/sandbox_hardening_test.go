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
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

func hardeningTestRunner(t *testing.T) *SandboxRunner {
	t.Helper()
	r := &SandboxRunner{
		cfg: SandboxConfig{
			Mode:       "bwrap",
			BwrapBin:   "/usr/bin/bwrap",
			PrlimitBin: "/usr/bin/prlimit",
		},
		logger:    slog.Default(),
		freshProc: true,
	}
	r.cfg.setDefaults()
	return r
}

func findFlagPair(args []string, flag, value string) int {
	for i := range len(args) - 1 {
		if args[i] == flag && args[i+1] == value {
			return i
		}
	}
	return -1
}

func findFlagTriple(args []string, flag, src, dst string) int {
	for i := range len(args) - 2 {
		if args[i] == flag && args[i+1] == src && args[i+2] == dst {
			return i
		}
	}
	return -1
}

func TestBuildBwrapCommand_PIDCgroupProcIsolation(t *testing.T) {
	runner := hardeningTestRunner(t)
	entry := &ManifestEntry{ToolName: "test-tool", SandboxProfile: "standard"}

	cmd, err := runner.buildBwrapCommand(context.Background(), entry, []string{"/tmp/args.json"}, "/tmp/work", nil, "", slog.Default())
	if err != nil {
		t.Fatalf("buildBwrapCommand: %v", err)
	}
	args := cmd.Args

	expectFlag(t, args, "--unshare-pid")
	expectFlag(t, args, "--unshare-cgroup-try")
	if findFlagPair(args, "--proc", "/proc") < 0 {
		t.Errorf("expected '--proc /proc' pair in args: %v", args)
	}
	for _, a := range args {
		if a == "--unshare-cgroup" {
			t.Errorf("must not emit --unshare-cgroup (gVisor unsupported); use --unshare-cgroup-try. args=%v", args)
		}
	}
}

type rootedStore struct {
	volume.Service
	root string
}

func (s rootedStore) Root() string { return s.root }

func TestBuildBwrapCommand_HidesOtherVolumesBehindTheVolumeRoot(t *testing.T) {
	root := "/app/data/volumes"
	runner := hardeningTestRunner(t)
	runner.volumeSvc = rootedStore{root: root}
	entry := &ManifestEntry{ToolName: "test-tool", SandboxProfile: "standard"}
	own := filepath.Join(root, "vol-mine", "data")
	vols := []resolvedVolume{{binding: remotetool.VolumeBinding{VolumeID: "vol-mine", MountPath: "/tmp/ws"}, hostPath: own}}

	cmd, err := runner.buildBwrapCommand(context.Background(), entry, []string{"/tmp/args.json"}, "/tmp/work", vols, "", slog.Default())
	if err != nil {
		t.Fatalf("buildBwrapCommand: %v", err)
	}
	mask := findFlagPair(cmd.Args, "--tmpfs", root)
	bind := findFlagTriple(cmd.Args, "--bind", own, "/tmp/ws")
	if mask < 0 || bind < 0 || mask > bind {
		t.Errorf("want the volume root masked before the call's own volume is bound; mask=%d bind=%d args=%v", mask, bind, cmd.Args)
	}

	runner.volumeSvc = rootedStore{root: "/tmp/sam-volumes"}
	cmd, err = runner.buildBwrapCommand(context.Background(), entry, []string{"/tmp/args.json"}, "/tmp/work", vols, "", slog.Default())
	if err != nil {
		t.Fatalf("buildBwrapCommand: %v", err)
	}
	if findFlagPair(cmd.Args, "--tmpfs", "/tmp/sam-volumes") >= 0 {
		t.Errorf("a root under /tmp is already hidden by the /tmp tmpfs; args=%v", cmd.Args)
	}
}

func TestBuildBwrapCommand_HidesOtherVolumesInBuiltinModeToo(t *testing.T) {
	root := filepath.Join(t.TempDir(), "volumes")
	if underAnyPrefix(root, []string{"/tmp"}) {
		t.Skip("the test's temp dir is under /tmp, which the sandbox already hides")
	}
	store, err := volume.NewFilesystemStore(root, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	runner := hardeningTestRunner(t)
	runner.volumeSvc = store

	cmd, err := runner.buildBwrapCommand(context.Background(), &ManifestEntry{ToolName: "test-tool", SandboxProfile: "standard"},
		[]string{"/tmp/args.json"}, "/tmp/work", nil, "", slog.Default())
	if err != nil {
		t.Fatalf("buildBwrapCommand: %v", err)
	}
	if findFlagPair(cmd.Args, "--tmpfs", root) < 0 {
		t.Errorf("the builtin-mode volume store's root is not masked; args=%v", cmd.Args)
	}
}

func TestBuildBwrapCommand_NoStaticProcNetMasks(t *testing.T) {
	runner := hardeningTestRunner(t)
	entry := &ManifestEntry{ToolName: "test-tool", SandboxProfile: "standard"}

	cmd, err := runner.buildBwrapCommand(context.Background(), entry, []string{"/tmp/args.json"}, "/tmp/work", nil, "", slog.Default())
	if err != nil {
		t.Fatalf("buildBwrapCommand: %v", err)
	}
	args := cmd.Args

	for i := range len(args) - 2 {
		if args[i] == "--ro-bind" && args[i+1] == "/dev/null" && strings.HasPrefix(args[i+2], "/proc/net/") {
			t.Errorf("must not emit static /proc/net mask %q; symlink resolution defeats it", args[i+2])
		}
	}
	if findFlagPair(args, "--tmpfs", "/sys/class/net") >= 0 {
		t.Errorf("must not unconditionally tmpfs /sys/class/net; path is absent on gVisor runtimes and bwrap mkdir is rejected")
	}
}

func extraMountBinding(args []string, mountPath string) (flag, hostPath string) {
	for i := range len(args) - 2 {
		if (args[i] == "--bind" || args[i] == "--ro-bind") && args[i+2] == mountPath {
			return args[i], args[i+1]
		}
	}
	return "", ""
}

func TestBuildBwrapCommand_ExtraMountResolvesSymlinks(t *testing.T) {
	target := t.TempDir()
	linkDir := t.TempDir()
	link := filepath.Join(linkDir, "alias")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	runner := hardeningTestRunner(t)
	entry := &ManifestEntry{
		ToolName:       "test-tool",
		SandboxProfile: "standard",
		ExtraMounts: []ExtraMount{
			{HostPath: link, MountPath: "/tmp/skill", ReadOnly: true},
		},
	}

	cmd, err := runner.buildBwrapCommand(context.Background(), entry, []string{"/tmp/args.json"}, "/tmp/work", nil, "", slog.Default())
	if err != nil {
		t.Fatalf("buildBwrapCommand: %v", err)
	}
	flag, emitted := extraMountBinding(cmd.Args, "/tmp/skill")
	if emitted == "" {
		t.Fatalf("expected a bind for /tmp/skill, got none. args=%v", cmd.Args)
	}
	if flag != "--ro-bind" {
		t.Errorf("expected --ro-bind for ReadOnly mount, got %q", flag)
	}
	if emitted == link {
		t.Errorf("expected resolved target as host path, got the symlink %q", emitted)
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("EvalSymlinks target: %v", err)
	}
	if emitted != resolvedTarget {
		t.Errorf("expected host path %q, got %q", resolvedTarget, emitted)
	}
}

func TestBuildBwrapCommand_ExtraMountSkippedWhenMissing(t *testing.T) {
	runner := hardeningTestRunner(t)
	entry := &ManifestEntry{
		ToolName:       "test-tool",
		SandboxProfile: "standard",
		ExtraMounts: []ExtraMount{
			{HostPath: "/nonexistent/path/that/does/not/exist", MountPath: "/tmp/missing", ReadOnly: true},
		},
	}

	cmd, err := runner.buildBwrapCommand(context.Background(), entry, []string{"/tmp/args.json"}, "/tmp/work", nil, "", slog.Default())
	if err != nil {
		t.Fatalf("buildBwrapCommand: %v", err)
	}
	if _, emitted := extraMountBinding(cmd.Args, "/tmp/missing"); emitted != "" {
		t.Errorf("expected no bind for non-existent host path; got bind from %q", emitted)
	}
}

func TestBuildBwrapCommand_ExtraMountPassThroughWhenNoSymlink(t *testing.T) {
	dir := t.TempDir()
	runner := hardeningTestRunner(t)
	entry := &ManifestEntry{
		ToolName:       "test-tool",
		SandboxProfile: "standard",
		ExtraMounts: []ExtraMount{
			{HostPath: dir, MountPath: "/tmp/plain", ReadOnly: false},
		},
	}

	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	entry.ExtraMounts[0].HostPath = resolved

	cmd, err := runner.buildBwrapCommand(context.Background(), entry, []string{"/tmp/args.json"}, "/tmp/work", nil, "", slog.Default())
	if err != nil {
		t.Fatalf("buildBwrapCommand: %v", err)
	}
	flag, emitted := extraMountBinding(cmd.Args, "/tmp/plain")
	if emitted != resolved {
		t.Errorf("expected bind from %q, got %q", resolved, emitted)
	}
	if flag != "--bind" {
		t.Errorf("expected --bind for writable mount, got %q", flag)
	}
}

func TestBuildBwrapCommand_ExtraMountFlagMatchesReadOnly(t *testing.T) {
	roDir := t.TempDir()
	rwDir := t.TempDir()

	runner := hardeningTestRunner(t)
	entry := &ManifestEntry{
		ToolName:       "test-tool",
		SandboxProfile: "standard",
		ExtraMounts: []ExtraMount{
			{HostPath: roDir, MountPath: "/tmp/ro", ReadOnly: true},
			{HostPath: rwDir, MountPath: "/tmp/rw", ReadOnly: false},
		},
	}

	cmd, err := runner.buildBwrapCommand(context.Background(), entry, []string{"/tmp/args.json"}, "/tmp/work", nil, "", slog.Default())
	if err != nil {
		t.Fatalf("buildBwrapCommand: %v", err)
	}

	if flag, _ := extraMountBinding(cmd.Args, "/tmp/ro"); flag != "--ro-bind" {
		t.Errorf("ReadOnly=true should emit --ro-bind, got %q", flag)
	}
	if flag, _ := extraMountBinding(cmd.Args, "/tmp/rw"); flag != "--bind" {
		t.Errorf("ReadOnly=false should emit --bind, got %q", flag)
	}
}

func indexOf(args []string, target string) int {
	for i, a := range args {
		if a == target {
			return i
		}
	}
	return -1
}

func TestBuildBwrapCommand_HardeningArgOrdering(t *testing.T) {
	runner := hardeningTestRunner(t)
	entry := &ManifestEntry{ToolName: "test-tool", SandboxProfile: "standard"}

	cmd, err := runner.buildBwrapCommand(context.Background(), entry, []string{"/tmp/args.json"}, "/tmp/work", nil, "", slog.Default())
	if err != nil {
		t.Fatalf("buildBwrapCommand: %v", err)
	}
	args := cmd.Args

	rootBind := findFlagTriple(args, "--ro-bind", "/", "/")
	workDirBind := findFlagTriple(args, "--bind", "/tmp/work", "/tmp/work")
	if rootBind < 0 || workDirBind < 0 {
		t.Fatalf("expected --ro-bind / / and --bind workdir anchors, got args=%v", args)
	}

	unsharePID := indexOf(args, "--unshare-pid")
	procPair := findFlagPair(args, "--proc", "/proc")
	if unsharePID < 0 || procPair < 0 {
		t.Fatalf("expected --unshare-pid and --proc /proc, got args=%v", args)
	}
	if procPair < unsharePID {
		t.Errorf("--proc /proc (idx %d) must come after --unshare-pid (idx %d)", procPair, unsharePID)
	}
	if procPair > workDirBind {
		t.Errorf("--proc /proc (idx %d) must come before --bind workdir (idx %d)", procPair, workDirBind)
	}
}

func TestBuildBwrapCommand_WrapperDoesNotCarryTheSTREnv(t *testing.T) {
	t.Setenv("STR_TEST_SECRET", "s3cret")
	runner := hardeningTestRunner(t)
	cmd, err := runner.buildBwrapCommand(t.Context(), &ManifestEntry{ToolName: "t", Executable: "/bin/true"},
		[]string{"/tmp/args.json"}, t.TempDir(), nil, "", slog.Default())
	if err != nil {
		t.Fatalf("buildBwrapCommand: %v", err)
	}
	if want := []string{"PATH=" + os.Getenv("PATH")}; !slices.Equal(cmd.Env, want) {
		t.Fatalf("wrapper env = %q, want only PATH", cmd.Env)
	}
}
