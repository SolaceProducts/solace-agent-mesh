// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package build

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/bundle"
	"github.com/SolaceProducts/solace-agent-mesh/internal/sdkvendor"
)

type Kind int

const (
	KindNone Kind = iota
	KindScript
	KindGoConvention
	KindPythonConvention
)

func (k Kind) String() string {
	switch k {
	case KindScript:
		return "script"
	case KindGoConvention:
		return "go"
	case KindPythonConvention:
		return "python"
	default:
		return "none"
	}
}

type Plan struct {
	Kind               Kind
	SrcDir             string
	Name               string
	ScriptPath         string
	GoEntry            string
	PythonRequirements string
}

var excludedHashDirs = map[string]struct{}{
	"dist":         {},
	".sam-cache":   {},
	"vendor":       {},
	"_sdk":         {},
	".deps":        {},
	"python":       {},
	".git":         {},
	"node_modules": {},
	"__pycache__":  {},
}

func Resolve(name, srcDir string) (Plan, error) {
	plan := Plan{SrcDir: srcDir, Name: name}

	hostScript, otherScript := scriptNamesForHost()

	hostPath := filepath.Join(srcDir, hostScript)
	otherPath := filepath.Join(srcDir, otherScript)
	hostExists := fileExists(hostPath)
	otherExists := fileExists(otherPath)

	if hostExists {
		plan.Kind = KindScript
		plan.ScriptPath = hostPath
		return plan, nil
	}
	if otherExists {
		return Plan{}, fmt.Errorf("toolset %q has %s but no %s — not buildable on %s",
			name, otherScript, hostScript, runtime.GOOS)
	}

	if fileExists(filepath.Join(srcDir, "go.mod")) {
		entry, err := resolveGoEntry(srcDir, name)
		if err != nil {
			return Plan{}, err
		}
		plan.Kind = KindGoConvention
		plan.GoEntry = entry
		return plan, nil
	}

	if fileExists(filepath.Join(srcDir, requirementsTxtFile)) {
		plan.Kind = KindPythonConvention
		plan.PythonRequirements = requirementsTxtFile
		return plan, nil
	}
	if fileExists(filepath.Join(srcDir, pyprojectFile)) {
		plan.Kind = KindPythonConvention
		plan.PythonRequirements = pyprojectFile
		return plan, nil
	}

	plan.Kind = KindNone
	return plan, nil
}

func scriptNamesForHost() (host, other string) {
	if runtime.GOOS == "windows" {
		return "build.bat", "build.sh"
	}
	return "build.sh", "build.bat"
}

func resolveGoEntry(srcDir, name string) (string, error) {
	cmdDir := filepath.Join(srcDir, "cmd")
	st, err := os.Stat(cmdDir)
	if err != nil || !st.IsDir() {
		return ".", nil
	}
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		return "", fmt.Errorf("read cmd/ for %s: %w", name, err)
	}
	subs := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			subs = append(subs, e.Name())
		}
	}
	switch len(subs) {
	case 0:
		return ".", nil
	case 1:
		return "./cmd/" + subs[0], nil
	default:
		return "", fmt.Errorf("toolset %q: ambiguous Go entry point — cmd/ has %d subdirectories (%s); provide build.sh + build.bat to disambiguate",
			name, len(subs), strings.Join(subs, ", "))
	}
}

type RunOptions struct {
	Stdout        io.Writer
	Stderr        io.Writer
	SDKVersion    string
	TargetOS      string
	TargetArch    string
	PythonVersion string
}

func vendorSDKIfNeeded(plan Plan, opts RunOptions) error {
	forceRefresh := os.Getenv("SAM_TOOL_SDK_REFRESH") == "1"

	var (
		injected bool
		err      error
		lang     string
	)
	switch {
	case fileExists(filepath.Join(plan.SrcDir, "go.mod")):
		if !goModRequiresSDK(filepath.Join(plan.SrcDir, "go.mod")) {
			return nil
		}
		lang = "go"
		if forceRefresh {
			err = sdkvendor.ForceGoSDK(plan.SrcDir)
			injected = err == nil
		} else {
			injected, err = sdkvendor.EnsureGoSDK(plan.SrcDir)
		}
	default:
		return nil
	}
	if err != nil {
		return fmt.Errorf("vendor %s SDK: %w", lang, err)
	}
	if injected && opts.Stderr != nil {
		_, _ = fmt.Fprintf(opts.Stderr, "sam: vendored samtoolsdk into %s/%s/ (was missing)\n",
			plan.Name, sdkvendor.VendorDirName())
	}
	return nil
}

func goModRequiresSDK(goModPath string) bool {
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk")
}

func Run(ctx context.Context, plan Plan, opts RunOptions) (string, error) {
	if plan.Kind == KindNone {
		return "", errors.New("Run: KindNone has no build step")
	}
	if plan.SrcDir == "" {
		return "", errors.New("Run: plan.SrcDir is empty")
	}

	if isPythonToolset(plan) {
		if err := checkToolPythonVersion(targetPythonVersion(opts)); err != nil {
			return "", err
		}
	}

	targetOS, targetArch := opts.resolvedTarget()
	target := Target{OS: targetOS, Arch: targetArch}
	distDir := filepath.Join(plan.SrcDir, "dist", target.Subpath())
	if err := os.RemoveAll(distDir); err != nil {
		return "", fmt.Errorf("clean dist: %w", err)
	}
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		return "", fmt.Errorf("create dist: %w", err)
	}

	if err := vendorSDKIfNeeded(plan, opts); err != nil {
		return "", err
	}

	switch plan.Kind {
	case KindScript:
		if err := runScript(ctx, plan, distDir, opts); err != nil {
			return "", err
		}
	case KindGoConvention:
		if err := runGoConvention(ctx, plan, distDir, opts); err != nil {
			return "", err
		}
	case KindPythonConvention:
		if err := runPythonConvention(ctx, plan, distDir, opts); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("Run: unknown plan kind %v", plan.Kind)
	}

	switch plan.Kind {
	case KindGoConvention:
		binName := plan.Name
		if targetOS == "windows" {
			binName += ".exe"
		}
		if err := verifyBinaryTarget(filepath.Join(distDir, binName), target); err != nil {
			return "", err
		}
	case KindScript:
		if err := verifyBinariesInDir(distDir, target); err != nil {
			return "", err
		}
	}
	if err := scrubPythonBuildPaths(distDir); err != nil {
		return "", fmt.Errorf("scrub build paths: %w", err)
	}
	if plan.Kind == KindPythonConvention {
		if err := verifyPythonSchema(ctx, plan, distDir, opts); err != nil {
			return "", err
		}
	}
	return distDir, nil
}

func runScript(ctx context.Context, plan Plan, distDir string, opts RunOptions) error {
	cmd := exec.CommandContext(ctx, plan.ScriptPath)
	cmd.Dir = plan.SrcDir
	cmd.Env = appendBuildEnv(os.Environ(), plan, distDir, opts)
	return runWithOutput(cmd, opts, fmt.Sprintf("build script %s", filepath.Base(plan.ScriptPath)))
}

func runGoConvention(ctx context.Context, plan Plan, distDir string, opts RunOptions) error {
	targetOS, targetArch := opts.resolvedTarget()
	binName := plan.Name
	if targetOS == "windows" {
		binName += ".exe"
	}
	outPath := filepath.Join(distDir, binName)
	args := []string{"build", "-o", outPath, plan.GoEntry}
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = plan.SrcDir
	cmd.Env = appendBuildEnv(os.Environ(), plan, distDir, opts)
	cmd.Env = append(cmd.Env,
		"GOOS="+targetOS,
		"GOARCH="+targetArch,
		"CGO_ENABLED=0",
	)
	return runWithOutput(cmd, opts, fmt.Sprintf("go build (GOOS=%s GOARCH=%s)", targetOS, targetArch))
}

func goBuildTarget() (string, string) {
	targetOS := os.Getenv("SAM_TOOL_TARGET_OS")
	if targetOS == "" {
		targetOS = "linux"
	}
	targetArch := os.Getenv("SAM_TOOL_TARGET_ARCH")
	if targetArch == "" {
		targetArch = "arm64"
	}
	return targetOS, targetArch
}

func ResolveTarget(t Target) Target {
	osS, archS := goBuildTarget()
	if t.OS != "" {
		osS = t.OS
	}
	if t.Arch != "" {
		archS = t.Arch
	}
	return Target{OS: osS, Arch: archS}
}

func (opts RunOptions) resolvedTarget() (string, string) {
	osS, archS := goBuildTarget()
	if opts.TargetOS != "" {
		osS = opts.TargetOS
	}
	if opts.TargetArch != "" {
		archS = opts.TargetArch
	}
	return osS, archS
}

const MinToolPythonVersion = "3.10"

const defaultToolPythonVersion = "3.11"

func targetPythonVersion(opts RunOptions) string {
	v := opts.PythonVersion
	if v == "" {
		v = os.Getenv("SAM_TOOL_PYTHON_VERSION")
	}
	if v == "" {
		v = defaultToolPythonVersion
	}
	return v
}

func checkToolPythonVersion(pyVersion string) error {
	below, ok := pythonVersionBelow(pyVersion, MinToolPythonVersion)
	if !ok || !below {
		return nil
	}
	return fmt.Errorf("python toolset build targets Python %s, but SAM Python tools "+
		"require Python >= %s (sam-tool-sdk publishes no distribution for older "+
		"interpreters). The target version comes from the deployment's Secure Tool "+
		"Runtime, or from SAM_TOOL_PYTHON_VERSION when set; point it at a Python "+
		">= %s interpreter before deploying Python tools",
		pyVersion, MinToolPythonVersion, MinToolPythonVersion)
}

func isPythonToolset(plan Plan) bool {
	if plan.Kind == KindPythonConvention {
		return true
	}
	if plan.Kind == KindScript {
		return fileExists(filepath.Join(plan.SrcDir, pyprojectFile)) ||
			fileExists(filepath.Join(plan.SrcDir, requirementsTxtFile))
	}
	return false
}

func pythonVersionBelow(v, floor string) (below, ok bool) {
	vMaj, vMin, vok := pythonMajorMinor(v)
	fMaj, fMin, fok := pythonMajorMinor(floor)
	if !vok || !fok {
		return false, false
	}
	if vMaj != fMaj {
		return vMaj < fMaj, true
	}
	return vMin < fMin, true
}

func pythonMajorMinor(s string) (major, minor int, ok bool) {
	parts := strings.SplitN(s, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	var err1, err2 error
	major, err1 = strconv.Atoi(strings.TrimSpace(parts[0]))
	minor, err2 = strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return major, minor, true
}

func runPythonConvention(ctx context.Context, plan Plan, distDir string, opts RunOptions) error {
	pythonDir := filepath.Join(distDir, "python")
	if err := os.MkdirAll(pythonDir, 0o755); err != nil {
		return fmt.Errorf("create python layer dir: %w", err)
	}

	var args []string
	switch plan.PythonRequirements {
	case requirementsTxtFile:
		args = []string{"install", "--target", pythonDir, "-r",
			filepath.Join(plan.SrcDir, requirementsTxtFile)}
	case pyprojectFile:
		args = []string{"install", "--target", pythonDir, plan.SrcDir}
	default:
		return fmt.Errorf("python build: unknown requirements file %q", plan.PythonRequirements)
	}

	platformArgs, err := pipPlatformArgs(opts)
	if err != nil {
		return err
	}
	args = append(args, platformArgs...)

	pipArgs := append([]string{"-m", "pip"}, args...)
	cmd := exec.CommandContext(ctx, "python3", pipArgs...)
	cmd.Dir = plan.SrcDir
	cmd.Env = appendBuildEnv(os.Environ(), plan, distDir, opts)
	if err := runWithOutput(cmd, opts, "pip install"); err != nil {
		return err
	}

	if err := verifyNonEmptyDir(pythonDir); err != nil {
		return fmt.Errorf("python build produced no output: %w", err)
	}
	return nil
}

func verifyPythonSchema(ctx context.Context, plan Plan, distDir string, opts RunOptions) error {
	targetOS, targetArch := opts.resolvedTarget()
	if targetOS != runtime.GOOS || targetArch != runtime.GOARCH {
		return nil
	}
	pyVersion := targetPythonVersion(opts)
	if hostPythonMinor() != pyVersion {
		return nil
	}
	if plan.PythonRequirements != pyprojectFile {
		return nil
	}

	scripts, err := pyprojectScriptNames(filepath.Join(plan.SrcDir, pyprojectFile))
	if err != nil {
		return fmt.Errorf("python schema check: %w", err)
	}

	pythonDir := filepath.Join(distDir, "python")
	pythonPath := pythonDir + string(os.PathListSeparator) + distDir
	env := environWithSinglePythonPath(pythonPath)
	for _, name := range scripts {
		script := filepath.Join(pythonDir, "bin", name)
		cmd := exec.CommandContext(ctx, script, "--schema")
		cmd.Dir = distDir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("python tool %q is not discoverable: %s --schema failed: %w\n%s",
				name, script, err, lastLines(out, 20))
		}
	}
	return nil
}

func environWithSinglePythonPath(pythonPath string) []string {
	return append(slices.DeleteFunc(slices.Clone(os.Environ()), func(s string) bool {
		return strings.HasPrefix(s, "PYTHONPATH=")
	}), "PYTHONPATH="+pythonPath)
}

func pyprojectScriptNames(pyprojectPath string) ([]string, error) {
	data, err := os.ReadFile(pyprojectPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", pyprojectPath, err)
	}
	var names []string
	inScripts := false
	for line := range strings.SplitSeq(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			inScripts = trimmed == "[project.scripts]"
			continue
		}
		if !inScripts {
			continue
		}
		key, _, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		name := strings.Trim(strings.TrimSpace(key), `"'`)
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

func lastLines(b []byte, n int) string {
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

const portablePythonShebang = "#!/usr/bin/env python3\n"

func scrubPythonBuildPaths(distDir string) error {
	pythonDir := filepath.Join(distDir, "python")
	entries, err := readDirIfPresent(pythonDir)
	if err != nil {
		return err
	}
	rewritten, err := rewritePythonShebangs(filepath.Join(pythonDir, "bin"))
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), ".dist-info") {
			if err := scrubDistInfo(filepath.Join(pythonDir, e.Name()), rewritten); err != nil {
				return err
			}
		}
	}
	return nil
}

func rewritePythonShebangs(binDir string) (map[string][]byte, error) {
	entries, err := readDirIfPresent(binDir)
	if err != nil {
		return nil, err
	}
	rewritten := map[string][]byte{}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		path := filepath.Join(binDir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		portable, ok := withPortablePythonShebang(data)
		if !ok || bytes.Equal(portable, data) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, portable, info.Mode().Perm()); err != nil {
			return nil, err
		}
		rewritten[path] = portable
	}
	return rewritten, nil
}

func readDirIfPresent(dir string) ([]fs.DirEntry, error) {
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	return os.ReadDir(dir)
}

func withPortablePythonShebang(script []byte) (_ []byte, ok bool) {
	if !bytes.HasPrefix(script, []byte("#!")) {
		return nil, false
	}
	lines := strings.SplitAfterN(string(script), "\n", 4)
	first := strings.TrimRight(lines[0], "\r\n")
	if first == "#!/bin/sh" && len(lines) == 4 &&
		strings.HasPrefix(lines[1], "'''exec' ") && strings.TrimRight(lines[2], "\r\n") == "' '''" {
		return []byte(portablePythonShebang + lines[3]), true
	}
	fields := strings.Fields(first[2:])
	if len(fields) > 1 && filepath.Base(fields[0]) == "env" {
		fields = fields[1:]
	}
	if len(fields) == 0 || !strings.HasPrefix(filepath.Base(fields[0]), "python") {
		return nil, false
	}
	return []byte(portablePythonShebang + strings.Join(lines[1:], "")), true
}

func scrubDistInfo(distInfoDir string, rewritten map[string][]byte) error {
	directURL := filepath.Join(distInfoDir, "direct_url.json")
	err := os.Remove(directURL)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	removed := err == nil
	if !removed && len(rewritten) == 0 {
		return nil
	}
	recordPath := filepath.Join(distInfoDir, "RECORD")
	data, err := os.ReadFile(recordPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return fmt.Errorf("parse %s: %w", recordPath, err)
	}
	installDir := filepath.Dir(distInfoDir)
	kept := rows[:0]
	changed := false
	for _, row := range rows {
		path := recordRowPath(installDir, row[0])
		if path == directURL {
			changed = true
			continue
		}
		if content, ok := rewritten[path]; ok {
			row = []string{row[0], recordHash(content), strconv.Itoa(len(content))}
			changed = true
		}
		kept = append(kept, row)
	}
	if !changed {
		return nil
	}
	var out bytes.Buffer
	w := csv.NewWriter(&out)
	w.UseCRLF = true
	if err := w.WriteAll(kept); err != nil {
		return err
	}
	return os.WriteFile(recordPath, out.Bytes(), 0o644)
}

func recordHash(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256=" + base64.RawURLEncoding.EncodeToString(sum[:])
}

const pipConsoleScriptRowPrefix = "../../bin/"

func recordRowPath(installDir, row string) string {
	if name, ok := strings.CutPrefix(row, pipConsoleScriptRowPrefix); ok {
		return filepath.Join(installDir, "bin", filepath.FromSlash(name))
	}
	return filepath.Join(installDir, filepath.FromSlash(row))
}

func verifyNonEmptyDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read %s: %w", dir, err)
	}
	if len(entries) == 0 {
		return fmt.Errorf("%s is empty", dir)
	}
	return nil
}

func pipPlatformArgs(opts RunOptions) ([]string, error) {
	targetOS, targetArch := opts.resolvedTarget()

	pyVersion := targetPythonVersion(opts)

	archOSMatch := targetOS == runtime.GOOS && targetArch == runtime.GOARCH
	if archOSMatch {
		if hostPythonMinor() == pyVersion {
			return nil, nil
		}
	}

	var platform string
	switch targetOS + "-" + targetArch {
	case "linux-arm64":
		platform = "manylinux2014_aarch64"
	case "linux-amd64":
		platform = "manylinux2014_x86_64"
	case "darwin-arm64":
		platform = "macosx_11_0_arm64"
	case "darwin-amd64":
		platform = "macosx_10_9_x86_64"
	default:
		return nil, fmt.Errorf("python cross-compile: unsupported target %s/%s "+
			"(no pip --platform mapping); set SAM_TOOL_TARGET_OS/SAM_TOOL_TARGET_ARCH "+
			"to a supported pair (linux/arm64, linux/amd64, darwin/arm64, darwin/amd64) "+
			"or supply a build.sh that handles this target", targetOS, targetArch)
	}
	return []string{
		"--platform", platform,
		"--python-version", pyVersion,
		"--only-binary=:all:",
	}, nil
}

func hostPythonMinor() string {
	out, err := exec.Command("python3", "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(out))
	after, ok := strings.CutPrefix(s, "Python ")
	if !ok {
		return ""
	}
	parts := strings.SplitN(after, ".", 3)
	if len(parts) < 2 {
		return ""
	}
	minor := parts[1]
	for i, c := range minor {
		if c < '0' || c > '9' {
			minor = minor[:i]
			break
		}
	}
	if minor == "" {
		return ""
	}
	return parts[0] + "." + minor
}

func appendBuildEnv(env []string, plan Plan, distDir string, opts RunOptions) []string {
	out := slices.Clone(env)
	out = append(out,
		"SAM_TOOL_NAME="+plan.Name,
		"SAM_TOOL_BUILD_OUT="+distDir,
	)
	if opts.SDKVersion != "" {
		out = append(out, "SAM_TOOL_SDK_VERSION="+opts.SDKVersion)
	}
	targetOS, targetArch := opts.resolvedTarget()
	out = append(out,
		"SAM_TOOL_TARGET_OS="+targetOS,
		"SAM_TOOL_TARGET_ARCH="+targetArch,
	)
	if opts.PythonVersion != "" {
		out = append(out, "SAM_TOOL_PYTHON_VERSION="+opts.PythonVersion)
	}
	goFlags := reproducibleGoFlags
	if prev := effectiveGoFlags(env); prev != "" {
		goFlags = prev + " " + goFlags
	}
	return append(out, "GOFLAGS="+goFlags)
}

const reproducibleGoFlags = "-trimpath -buildvcs=false"

func effectiveGoFlags(env []string) string {
	if env == nil {
		env = os.Environ()
	}
	cmd := exec.Command("go", "env", "GOFLAGS")
	cmd.Env = append(slices.Clone(env), "GOTOOLCHAIN=local")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func runWithOutput(cmd *exec.Cmd, opts RunOptions, label string) error {
	const tailLines = 50
	tail := newLineRing(tailLines)

	cmd.Stdout = teeWriter(opts.Stdout, tail)
	cmd.Stderr = teeWriter(opts.Stderr, tail)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w\n--- last %d lines of output ---\n%s",
			label, err, tailLines, tail.String())
	}
	return nil
}

func Hash(plan Plan, target Target, srcDir string) (string, error) {
	return hashWithBundleVersion(plan, target, srcDir, bundle.FormatVersion)
}

func hashWithBundleVersion(plan Plan, target Target, srcDir string, bundleVersion int) (string, error) {
	h := sha256.New()
	_, _ = h.Write([]byte("v1\x00kind="))
	_, _ = h.Write([]byte(plan.Kind.String()))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte("target="))
	_, _ = h.Write([]byte(target.Subpath()))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte("bundle="))
	_, _ = h.Write([]byte(strconv.Itoa(bundleVersion)))
	_, _ = h.Write([]byte{0})

	files, err := collectHashFiles(srcDir)
	if err != nil {
		return "", err
	}

	var lenBuf [8]byte
	for _, f := range files {
		binary.BigEndian.PutUint64(lenBuf[:], uint64(len(f.relPath)))
		_, _ = h.Write(lenBuf[:])
		_, _ = h.Write([]byte(f.relPath))

		binary.BigEndian.PutUint32(lenBuf[:4], uint32(f.mode))
		_, _ = h.Write(lenBuf[:4])

		binary.BigEndian.PutUint64(lenBuf[:], uint64(f.size))
		_, _ = h.Write(lenBuf[:])
		if err := streamFile(filepath.Join(srcDir, filepath.FromSlash(f.relPath)), h); err != nil {
			return "", fmt.Errorf("hash %s: %w", f.relPath, err)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type hashEntry struct {
	relPath string
	mode    os.FileMode
	size    int64
}

func collectHashFiles(srcDir string) ([]hashEntry, error) {
	var out []hashEntry
	walkErr := filepath.WalkDir(srcDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == srcDir {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if _, skip := excludedHashDirs[name]; skip {
				return filepath.SkipDir
			}
			return nil
		}
		if name == ".DS_Store" {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, hashEntry{
			relPath: filepath.ToSlash(rel),
			mode:    info.Mode().Perm(),
			size:    info.Size(),
		})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	slices.SortFunc(out, func(a, b hashEntry) int { return strings.Compare(a.relPath, b.relPath) })
	return out, nil
}

func streamFile(path string, w io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(w, f)
	return err
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !st.IsDir()
}

func teeWriter(ws ...io.Writer) io.Writer {
	live := ws[:0]
	for _, w := range ws {
		if w != nil {
			live = append(live, w)
		}
	}
	switch len(live) {
	case 0:
		return io.Discard
	case 1:
		return live[0]
	default:
		out := make([]io.Writer, len(live))
		copy(out, live)
		return io.MultiWriter(out...)
	}
}

type lineRing struct {
	max     int
	lines   []string
	pending strings.Builder
}

func newLineRing(max int) *lineRing {
	return &lineRing{max: max}
}

func (l *lineRing) Write(p []byte) (int, error) {
	scanner := bufio.NewScanner(strings.NewReader(l.pending.String() + string(p)))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	scanner.Split(bufio.ScanLines)
	l.pending.Reset()
	last := ""
	for scanner.Scan() {
		last = scanner.Text()
		l.append(last)
	}
	if len(p) > 0 && p[len(p)-1] != '\n' && last != "" {
		if len(l.lines) > 0 && l.lines[len(l.lines)-1] == last {
			l.lines = l.lines[:len(l.lines)-1]
		}
		l.pending.WriteString(last)
	}
	return len(p), nil
}

func (l *lineRing) append(line string) {
	l.lines = append(l.lines, line)
	if len(l.lines) > l.max {
		l.lines = l.lines[len(l.lines)-l.max:]
	}
}

func (l *lineRing) String() string {
	if l.pending.Len() > 0 {
		out := append(slices.Clone(l.lines), l.pending.String())
		return strings.Join(out, "\n")
	}
	return strings.Join(l.lines, "\n")
}

const (
	requirementsTxtFile = "requirements.txt"
	pyprojectFile       = "pyproject.toml"
)
