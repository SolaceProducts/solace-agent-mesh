// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const discoverStderrCap = 4096

const schemaTimeout = 30 * time.Second

const schemaProbeRetries = 3

const schemaProbeRetryDelay = 50 * time.Millisecond

func DiscoverFromExecutable(executable, pythonPath string) (map[string]DiscoveredTool, error) {
	return probeSchema(nil, executable, "", pythonPath, nil, "--schema")
}

func probeSchema(runner *SandboxRunner, executable, toolDir, pythonPath string, readOnlyMounts []string, args ...string) (map[string]DiscoveredTool, error) {
	cmdline := strings.Join(toolInvocationArgv(executable, args...), " ")
	return probeWithTransientRetry(cmdline, func() (map[string]DiscoveredTool, error) {
		ctx, cancel := context.WithTimeout(context.Background(), schemaTimeout)
		defer cancel()
		var stdout, stderr bytes.Buffer
		err := runner.runInstallStep(ctx, installStep{
			name: filepath.Base(executable), toolDir: toolDir, executable: executable, args: args,
			readOnlyMounts: readOnlyMounts, pythonPath: pythonPath, stdout: &stdout, stderr: &stderr,
		})
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return nil, fmt.Errorf("running %s: timed out after %s%s", cmdline, schemaTimeout, formatStderrSuffix(stderr.Bytes()))
			}
			return nil, fmt.Errorf("running %s: %w%s", cmdline, err, formatStderrSuffix(stderr.Bytes()))
		}
		return parseSchemaOutput(stdout.Bytes(), executable)
	})
}

func probeWithTransientRetry(label string, probe func() (map[string]DiscoveredTool, error)) (map[string]DiscoveredTool, error) {
	var lastErr error
	for attempt := 0; attempt <= schemaProbeRetries; attempt++ {
		tools, err := probe()
		if err == nil {
			return tools, nil
		}
		if !isTransientProcessErr(err) {
			return nil, err
		}
		lastErr = err
		if attempt < schemaProbeRetries {
			time.Sleep(schemaProbeRetryDelay)
		}
	}
	return nil, fmt.Errorf("running %s: still failing after %d retries: %w",
		label, schemaProbeRetries, lastErr)
}

func isTransientProcessErr(err error) bool {
	return errors.Is(err, syscall.ECHILD) || errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.ETXTBSY)
}

func formatStderrSuffix(stderr []byte) string {
	trimmed := bytes.TrimRight(stderr, " \t\r\n")
	if len(trimmed) == 0 {
		return ""
	}
	if len(trimmed) > discoverStderrCap {
		trimmed = append([]byte("...(truncated)... "), trimmed[len(trimmed)-discoverStderrCap:]...)
	}
	return " (stderr: " + strings.ReplaceAll(string(trimmed), "\n", " | ") + ")"
}

func parseSchemaOutput(output []byte, label string) (map[string]DiscoveredTool, error) {
	var schema struct {
		Tools map[string]DiscoveredTool `json:"tools"`
	}
	if err := json.Unmarshal(output, &schema); err != nil {
		return nil, fmt.Errorf("parsing schema output from %s: %w", label, err)
	}
	if len(schema.Tools) == 0 {
		return nil, fmt.Errorf("no tools discovered from %s", label)
	}
	if _, declaresEmptyName := schema.Tools[""]; declaresEmptyName {
		nlog().Warn("schema output declares a tool with an empty name; dropping it",
			"source", label, "otherTools", len(schema.Tools)-1)
		delete(schema.Tools, "")
		if len(schema.Tools) == 0 {
			return nil, fmt.Errorf("no usable tools discovered from %s", label)
		}
	}
	return schema.Tools, nil
}

func envWithPythonPath(pythonPath string) []string {
	parent := os.Environ()
	out := make([]string, 0, len(parent)+1)
	for _, kv := range parent {
		if strings.HasPrefix(kv, "PYTHONPATH=") || strings.HasPrefix(kv, "PYTHONNOUSERSITE=") {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "PYTHONPATH="+pythonPath, "PYTHONNOUSERSITE=1")
	return out
}

type DiscoveredTool struct {
	Description    string                             `json:"description"`
	Parameters     map[string]any                     `json:"parameters,omitempty"`
	ArtifactParams map[string]discoveredArtifactParam `json:"artifact_params,omitempty"`
	Instructions   string                             `json:"instructions,omitempty"`
	Auth           *ToolDeclaredAuth                  `json:"auth,omitempty"`
	VolumeParams   []VolumeParam                      `json:"volume_params,omitempty"`
	ConfigSchema   []map[string]any                   `json:"config_schema,omitempty"`
	TimeoutSeconds int                                `json:"timeout_seconds,omitempty"`
	Effect         string                             `json:"effect,omitempty"`
}

const nonStringEffectMarker = "(non-string effect)"

func (d *DiscoveredTool) UnmarshalJSON(data []byte) error {
	type plain DiscoveredTool
	decoded := struct {
		*plain
		Effect json.RawMessage `json:"effect"`
	}{plain: (*plain)(d)}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	d.Effect = effectText(decoded.Effect)
	return nil
}

func effectText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return nonStringEffectMarker
	}
	return text
}

const unknownArch = "unknown-arch"

func isNativeBinary(path string) bool {
	return describeBinaryFormat(path) != ""
}

func describeBinaryFormat(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	hdr := make([]byte, 64)
	n, err := f.Read(hdr)
	if err != nil || n < 4 {
		return ""
	}

	if d := describeELF(hdr, n); d != "" {
		return d
	}
	if d := describeMachO(hdr); d != "" {
		return d
	}
	if d := describePE(f, hdr, n); d != "" {
		return d
	}
	return ""
}

func describeELF(hdr []byte, n int) string {
	if hdr[0] != 0x7f || hdr[1] != 'E' || hdr[2] != 'L' || hdr[3] != 'F' {
		return ""
	}
	arch := unknownArch
	if n >= 20 {
		switch uint16(hdr[18]) | uint16(hdr[19])<<8 {
		case 0x3e:
			arch = "x86_64"
		case 0xb7:
			arch = "aarch64"
		case 0x03:
			arch = "i386"
		case 0x28:
			arch = "armv7"
		case 0xf3:
			arch = "riscv64"
		}
	}
	return "Linux ELF (" + arch + ")"
}

func describeMachO(hdr []byte) string {
	if hdr[0] == 0xfe && hdr[1] == 0xed && hdr[2] == 0xfa && (hdr[3] == 0xce || hdr[3] == 0xcf) {
		return "macOS Mach-O"
	}
	if hdr[3] == 0xfe && hdr[2] == 0xed && hdr[1] == 0xfa && (hdr[0] == 0xce || hdr[0] == 0xcf) {
		return "macOS Mach-O"
	}
	return ""
}

func describePE(f *os.File, hdr []byte, n int) string {
	if hdr[0] != 0x4d || hdr[1] != 0x5a {
		return ""
	}
	return "Windows PE (" + peArch(f, hdr, n) + ")"
}

func peArch(f *os.File, hdr []byte, n int) string {
	if n < 0x40 {
		return unknownArch
	}
	off := uint32(hdr[0x3c]) | uint32(hdr[0x3d])<<8 | uint32(hdr[0x3e])<<16 | uint32(hdr[0x3f])<<24
	peHdr := make([]byte, 6)
	if _, err := f.ReadAt(peHdr, int64(off)); err != nil {
		return unknownArch
	}
	if peHdr[0] != 'P' || peHdr[1] != 'E' || peHdr[2] != 0 || peHdr[3] != 0 {
		return unknownArch
	}
	switch uint16(peHdr[4]) | uint16(peHdr[5])<<8 {
	case 0x8664:
		return "x86_64"
	case 0xaa64:
		return "aarch64"
	case 0x014c:
		return "i386"
	case 0x01c4:
		return "armv7"
	}
	return unknownArch
}

func explainExecFormatError(executable string, err error) error {
	if err == nil || !strings.Contains(err.Error(), "exec format error") {
		return err
	}
	binFmt := describeBinaryFormat(executable)
	if binFmt == "" {
		binFmt = "an unrecognized binary format"
	}
	host := runtimeGOOS() + "/" + runtimeGOARCH()
	return fmt.Errorf("binary cannot run on this host: it's %s, but STR is running on %s. "+
		"Re-upload the toolset with a binary built for %s "+
		"(Go example: `GOOS=%s GOARCH=%s go build`), "+
		"or run STR in container mode to provide the binary's target runtime. "+
		"Underlying error: %w",
		binFmt, host, host, runtimeGOOS(), runtimeGOARCH(), err)
}

var (
	runtimeGOOS   = func() string { return runtime.GOOS }
	runtimeGOARCH = func() string { return runtime.GOARCH }
)

func hostExecCompatError(executable string) error {
	goos, goarch := detectBinaryTarget(executable)
	osMismatch := goos != "" && goos != runtimeGOOS()
	archMismatch := goarch != "" && goarch != runtimeGOARCH()
	if !osMismatch && !archMismatch {
		return nil
	}
	return explainExecFormatError(executable, fmt.Errorf("fork/exec %s: exec format error", executable))
}

func detectBinaryTarget(path string) (goos, goarch string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer func() { _ = f.Close() }()

	hdr := make([]byte, 64)
	n, err := f.Read(hdr)
	if err != nil || n < 4 {
		return "", ""
	}

	if hdr[0] == 0x7f && hdr[1] == 'E' && hdr[2] == 'L' && hdr[3] == 'F' {
		return "linux", elfGOARCH(hdr, n)
	}
	if isMachOMagic(hdr) {
		return "darwin", machOGOARCH(hdr)
	}
	if hdr[0] == 0x4d && hdr[1] == 0x5a {
		return "windows", peGOARCH(f, hdr, n)
	}
	return "", ""
}

func elfGOARCH(hdr []byte, n int) string {
	if n < 20 {
		return ""
	}
	switch uint16(hdr[18]) | uint16(hdr[19])<<8 {
	case 0x3e:
		return "amd64"
	case 0xb7:
		return "arm64"
	case 0x03:
		return "386"
	case 0x28:
		return "arm"
	case 0xf3:
		return "riscv64"
	}
	return ""
}

func isMachOMagic(hdr []byte) bool {
	if hdr[0] == 0xfe && hdr[1] == 0xed && hdr[2] == 0xfa && (hdr[3] == 0xce || hdr[3] == 0xcf) {
		return true
	}
	return hdr[3] == 0xfe && hdr[2] == 0xed && hdr[1] == 0xfa && (hdr[0] == 0xce || hdr[0] == 0xcf)
}

func machOGOARCH(hdr []byte) string {
	if len(hdr) < 8 {
		return ""
	}
	var cputype uint32
	if hdr[0] == 0xfe {
		cputype = uint32(hdr[4])<<24 | uint32(hdr[5])<<16 | uint32(hdr[6])<<8 | uint32(hdr[7])
	} else {
		cputype = uint32(hdr[4]) | uint32(hdr[5])<<8 | uint32(hdr[6])<<16 | uint32(hdr[7])<<24
	}
	switch cputype {
	case 0x01000007:
		return "amd64"
	case 0x0100000c:
		return "arm64"
	}
	return ""
}

func peGOARCH(f *os.File, hdr []byte, n int) string {
	if n < 0x40 {
		return ""
	}
	off := uint32(hdr[0x3c]) | uint32(hdr[0x3d])<<8 | uint32(hdr[0x3e])<<16 | uint32(hdr[0x3f])<<24
	peHdr := make([]byte, 6)
	if _, err := f.ReadAt(peHdr, int64(off)); err != nil {
		return ""
	}
	if peHdr[0] != 'P' || peHdr[1] != 'E' || peHdr[2] != 0 || peHdr[3] != 0 {
		return ""
	}
	switch uint16(peHdr[4]) | uint16(peHdr[5])<<8 {
	case 0x8664:
		return "amd64"
	case 0xaa64:
		return "arm64"
	case 0x014c:
		return "386"
	case 0x01c4:
		return "arm"
	}
	return ""
}

var nativeExtMissingRe = regexp.MustCompile(`No module named '[^']*\._[^']*'`)

func classifyUnavailableExecutable(execPath string) string {
	if execPath == "" || execPath == "(none)" {
		return "no executable path configured for this tool in the skill manifest"
	}
	pythonScript := strings.EqualFold(filepath.Ext(execPath), ".py")
	info, statErr := os.Stat(execPath)
	if statErr != nil {
		if pythonScript {
			return fmt.Sprintf("executable %q not found: the skill bundle does not ship this file — "+
				"check the tool's `executable` entry in tools/manifest.yaml against the bundle contents",
				filepath.Base(execPath))
		}
		return fmt.Sprintf("executable %q not found: the skill bundle may have been packaged "+
			"without compiling the Go tool — add `runtime: go` to the tool's entry in "+
			"tools/manifest.yaml and repackage with `sam skill package`", filepath.Base(execPath))
	}
	if info.IsDir() {
		return fmt.Sprintf("executable path %q is a directory, not a binary", filepath.Base(execPath))
	}
	if pythonScript {
		return fmt.Sprintf("Python tool %q exists but is missing its executable bit (mode %s): the "+
			"bundle was likely packaged on a host that cannot record Unix permissions (e.g. Windows) — "+
			"restore it with chmod +x before zipping, or repackage on macOS/Linux with `sam skill package`",
			filepath.Base(execPath), info.Mode())
	}
	return fmt.Sprintf("executable %q exists but is not runnable (mode %s): "+
		"the skill bundle likely ships Go source instead of a compiled binary — "+
		"add `runtime: go` to the tool's entry in tools/manifest.yaml and "+
		"repackage with `sam skill package`", filepath.Base(execPath), info.Mode())
}

func explainDiscoveryError(executable string, err error) error {
	if err == nil {
		return nil
	}
	if out := explainExecFormatError(executable, err); out != err {
		return out
	}
	pythonPath, pythonVersion := resolveSTRPython()
	if out := explainUnusablePython(executable, pythonPath, pythonVersion, err); out != err {
		return out
	}
	if out := explainMissingInterpreter(executable, err); out != err {
		return out
	}
	return explainNativeABIMismatch(err)
}

func explainUnusablePython(executable, pythonPath, pythonVersion string, err error) error {
	if err == nil || !usesContainerPython(executable) {
		return err
	}
	if pythonAtLeast(pythonVersion, strPythonFloor) {
		return err
	}
	hint := "install Python " + strPythonFloor + " or newer, then re-run discovery"
	if strings.TrimSpace(os.Getenv(strPythonOverrideEnv)) == "" {
		hint += " (or set " + strPythonOverrideEnv + " to one)"
	} else {
		hint = strPythonOverrideEnv + " is set but does not resolve to a usable Python " +
			strPythonFloor + "+; fix it, or unset it to use auto-detection, then re-run discovery"
	}
	switch {
	case pythonVersion != "":
		return fmt.Errorf("this tool runs on the STR's Python interpreter, which resolved to %q at version %s — below the %s floor SAM Python tools require; %s. Underlying error: %w",
			pythonPath, pythonVersion, strPythonFloor, hint, err)
	case interpreterExists(pythonPath):
		return fmt.Errorf("this tool runs on the STR's Python interpreter, which resolved to %q, but that binary did not report a version and cannot run tools (on Windows this is typically the Microsoft Store placeholder alias); %s. Underlying error: %w",
			pythonPath, hint, err)
	default:
		return fmt.Errorf("this tool requires Python %s or newer, which was not found on this host; %s. Underlying error: %w",
			strPythonFloor, hint, err)
	}
}

func explainMissingInterpreter(executable string, err error) error {
	if err == nil || !errors.Is(err, syscall.ENOENT) {
		return err
	}
	if _, statErr := os.Stat(executable); statErr != nil {
		return err
	}
	interp := pythonShebangInterpreter(executable)
	if interp == "" {
		return err
	}
	return fmt.Errorf("the tool's interpreter %q is not present in the STR runtime, so its "+
		"console script cannot launch; the bundle was likely built on a machine with a "+
		"different Python install. Rebuild and re-upload with `sam toolset package`. "+
		"Underlying error: %w", interp, err)
}

func explainNativeABIMismatch(err error) error {
	if err == nil || !nativeExtMissingRe.MatchString(err.Error()) {
		return err
	}
	ver := strPythonVersion()
	if ver == "" {
		return fmt.Errorf("a native (compiled) dependency was built for a different Python "+
			"version than the STR runtime; rebuild with "+
			"`SAM_TOOL_PYTHON_VERSION=<str-python-version> sam toolset package` to match the STR. "+
			"Underlying error: %w", err)
	}
	return fmt.Errorf("a native (compiled) dependency was built for a Python version other than "+
		"the STR runtime's Python %s; rebuild with `SAM_TOOL_PYTHON_VERSION=%s sam toolset package`. "+
		"Underlying error: %w", ver, ver, err)
}

var venvSkipList = map[string]bool{
	"python": true, "python3": true,
	"pip": true, "pip3": true,
	"wheel":    true,
	"activate": true, "activate.csh": true, "activate.fish": true, "activate.ps1": true,
	"chardetect": true, "normalizer": true,
	"pydoc": true, "pydoc3": true,
	"pyftmerge": true, "pyftsubset": true, "ttx": true, "fonttools": true,
	"dotenv": true, "httpx": true,
}

func isVenvInfraExecutable(name string) bool {
	base := name
	if runtimeGOOS() == "windows" {
		lower := strings.ToLower(name)
		base = strings.TrimSuffix(lower, filepath.Ext(lower))
	}
	if venvSkipList[base] {
		return true
	}
	for _, prefix := range []string{"python3.", "pip3."} {
		if strings.HasPrefix(base, prefix) {
			return true
		}
	}
	return false
}
