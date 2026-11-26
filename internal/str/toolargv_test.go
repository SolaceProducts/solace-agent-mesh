// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func writeRawFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestToolInvocationArgv_RerouteMissingPythonInterpreter(t *testing.T) {
	dir := t.TempDir()
	script := writeRawFile(t, dir, "mytool", "#!/nonexistent/opt/python@3.14/bin/python3.14\nprint('hi')\n")

	argv := toolInvocationArgv(script, "--schema")
	want := []string{containerPython(), script, "--schema"}
	if len(argv) != len(want) || argv[0] != want[0] || argv[1] != want[1] || argv[2] != want[2] {
		t.Fatalf("argv = %v; want %v (reroute through %s)", argv, want, containerPython())
	}
}

func TestToolInvocationArgv_HonorsExistingInterpreter(t *testing.T) {
	dir := t.TempDir()
	interp := writeRawFile(t, dir, "python3-fake", "#!/bin/sh\n")
	script := writeRawFile(t, dir, "mytool", "#!"+interp+"\nprint('hi')\n")

	argv := toolInvocationArgv(script, "--schema")
	if len(argv) != 2 || argv[0] != script || argv[1] != "--schema" {
		t.Fatalf("argv = %v; want [%s --schema] (interpreter exists, run directly)", argv, script)
	}
}

func TestToolInvocationArgv_EnvPythonShebangRerouted(t *testing.T) {
	dir := t.TempDir()
	script := writeRawFile(t, dir, "mytool", "#!/usr/bin/env python3\nprint('hi')\n")

	argv := toolInvocationArgv(script, "--schema")
	want := []string{containerPython(), script, "--schema"}
	if len(argv) != 3 || argv[0] != want[0] || argv[1] != want[1] || argv[2] != want[2] {
		t.Fatalf("argv = %v; want %v (env python reroutes through the resolved interpreter)", argv, want)
	}

	shell := writeRawFile(t, dir, "mytool.sh", "#!/usr/bin/env bash\necho hi\n")
	if argv := toolInvocationArgv(shell, "--schema"); len(argv) != 2 || argv[0] != shell {
		t.Fatalf("argv = %v; want the non-Python env script run directly", argv)
	}
}

func TestToolInvocationArgv_WindowsPyThroughResolvedInterpreter(t *testing.T) {
	fakeWindowsGOOS(t)

	dir := t.TempDir()
	for name, body := range map[string]string{
		"env_shebang.py": "#!/usr/bin/env python3\nprint('hi')\n",
		"no_shebang.py":  "print('hi')\n",
		"upper_ext.PY":   "print('hi')\n",
	} {
		script := writeRawFile(t, dir, name, body)
		argv := toolInvocationArgv(script, "--schema")
		want := []string{containerPython(), script, "--schema"}
		if len(argv) != 3 || argv[0] != want[0] || argv[1] != want[1] || argv[2] != want[2] {
			t.Errorf("%s: argv = %v; want %v", name, argv, want)
		}
	}
}

func TestToolInvocationArgv_WindowsNativeBinaryUntouched(t *testing.T) {
	fakeWindowsGOOS(t)

	dir := t.TempDir()
	bin := writeRawFile(t, dir, "mytool.exe", "MZnative-ish")
	argv := toolInvocationArgv(bin, "--schema")
	if len(argv) != 2 || argv[0] != bin {
		t.Fatalf("argv = %v; want the binary run directly", argv)
	}
}

func TestToolInvocationArgv_NonPythonMissingInterpreterNotRerouted(t *testing.T) {
	dir := t.TempDir()
	script := writeRawFile(t, dir, "mytool", "#!/nonexistent/bin/perl\nprint 1;\n")

	argv := toolInvocationArgv(script, "--schema")
	if len(argv) != 2 || argv[0] != script {
		t.Fatalf("argv = %v; want the script run directly (non-Python interpreter)", argv)
	}
}

func TestToolInvocationArgv_NativeBinaryRunsDirect(t *testing.T) {
	dir := t.TempDir()
	bin := writeRawFile(t, dir, "mytool", "\x7fELF\x02\x01\x01\x00native-ish")

	argv := toolInvocationArgv(bin, "--schema")
	if len(argv) != 2 || argv[0] != bin {
		t.Fatalf("argv = %v; want the binary run directly (no shebang)", argv)
	}
}

func TestPythonShebangInterpreter(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		body string
		want string
	}{
		{"abs python", "#!/opt/py/bin/python3.14\nx", "/opt/py/bin/python3.14"},
		{"env python", "#!/usr/bin/env python3\nx", "/usr/bin/env"},
		{"no shebang", "print('hi')\n", ""},
		{"native magic", "\x7fELF\x02\x01", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := writeRawFile(t, dir, "s_"+c.name, c.body)
			if got := pythonShebangInterpreter(p); got != c.want {
				t.Errorf("pythonShebangInterpreter = %q; want %q", got, c.want)
			}
		})
	}
	if got := pythonShebangInterpreter(filepath.Join(dir, "does-not-exist")); got != "" {
		t.Errorf("missing file = %q; want \"\"", got)
	}
}

func TestInterpreterExists(t *testing.T) {
	dir := t.TempDir()
	present := writeRawFile(t, dir, "present", "x")
	if !interpreterExists(present) {
		t.Errorf("interpreterExists(%q) = false; want true", present)
	}
	if interpreterExists(filepath.Join(dir, "absent")) {
		t.Error("interpreterExists on a missing abs path = true; want false")
	}
	if runtime.GOOS != "windows" {
		if _, err := exec.LookPath("sh"); err == nil && !interpreterExists("sh") {
			t.Error("interpreterExists(\"sh\") = false; want true (resolved via PATH)")
		}
	}
}

func TestIsPythonInterpreterName(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"/opt/py/bin/python3.14", true},
		{"/usr/local/bin/python", true},
		{"python3", true},
		{"/usr/bin/env", false},
		{"/bin/sh", false},
		{"/nonexistent/perl", false},
	} {
		if got := isPythonInterpreterName(c.in); got != c.want {
			t.Errorf("isPythonInterpreterName(%q) = %v; want %v", c.in, got, c.want)
		}
	}
}

func TestDiscoverFromExecutable_RoutesBadShebangThroughPython3(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shebang scenario")
	}
	if _, err := exec.LookPath(containerPython()); err != nil {
		t.Skipf("%s not available on this host", containerPython())
	}
	dir := t.TempDir()
	body := "#!/nonexistent/opt/python@3.14/bin/python3.14\n" +
		"import sys, json\n" +
		"if '--schema' in sys.argv:\n" +
		"    print(json.dumps({'tools': {'greet': {'description': 'hi'}}}))\n"
	script := writeRawFile(t, dir, "greeter", body)

	tools, err := DiscoverFromExecutable(script, "")
	if err != nil {
		t.Fatalf("DiscoverFromExecutable rerouted through %s should succeed; got %v", containerPython(), err)
	}
	if _, ok := tools["greet"]; !ok {
		t.Errorf("discovered tools = %v; want a 'greet' entry", tools)
	}
}

func TestToolInvocationArgv_NonWindowsPyKeepsVenvShebang(t *testing.T) {
	orig := runtimeGOOS
	runtimeGOOS = func() string { return "linux" }
	t.Cleanup(func() { runtimeGOOS = orig })

	dir := t.TempDir()
	interp := writeRawFile(t, dir, "python3.12", "#!/bin/sh\n")
	script := writeRawFile(t, dir, "mytool.py", "#!"+interp+"\nprint('hi')\n")

	argv := toolInvocationArgv(script, "--schema")
	if len(argv) != 2 || argv[0] != script {
		t.Fatalf("argv = %v; want [%s --schema] (venv shebang honored off Windows)", argv, script)
	}
}
