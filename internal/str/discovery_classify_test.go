// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
)

func enoentExec(path string) error {
	inner := &os.PathError{Op: "fork/exec", Path: path, Err: syscall.ENOENT}
	return fmt.Errorf("running %s --schema: %w", path, inner)
}

func TestExplainUnusablePython(t *testing.T) {
	dir := t.TempDir()

	const absentPython = "/nonexistent/python@9.9/bin/python3.99"

	t.Run("no python interpreter at all yields install-Python guidance", func(t *testing.T) {
		script := writeRawFile(t, dir, "pytool", "#!"+absentPython+"\nprint('hi')\n")
		out := explainUnusablePython(script, absentPython, "", enoentExec(script))
		msg := out.Error()
		if !strings.Contains(msg, "requires Python "+strPythonFloor) {
			t.Errorf("message should state Python %s is required; got %q", strPythonFloor, msg)
		}
		if !strings.Contains(msg, strPythonOverrideEnv) {
			t.Errorf("message should mention the %s override; got %q", strPythonOverrideEnv, msg)
		}
	})

	t.Run("no-op when the resolved python meets the floor", func(t *testing.T) {
		script := writeRawFile(t, dir, "pytool_ok", "#!"+absentPython+"\nprint('hi')\n")
		in := enoentExec(script)
		if out := explainUnusablePython(script, "python3", "3.12", in); out != in {
			t.Errorf("python meets the floor; want err unchanged, got %q", out)
		}
	})

	t.Run("below-floor python names the version and the floor", func(t *testing.T) {
		script := writeRawFile(t, dir, "pytool_old", "#!"+absentPython+"\nprint('hi')\n")
		out := explainUnusablePython(script, "/usr/bin/python3", "3.9", enoentExec(script))
		msg := out.Error()
		if !strings.Contains(msg, "3.9") || !strings.Contains(msg, strPythonFloor) {
			t.Errorf("message should name both the resolved 3.9 and the %s floor; got %q", strPythonFloor, msg)
		}
	})

	t.Run("interpreter that exists but reports no version is called out as dead", func(t *testing.T) {
		script := writeRawFile(t, dir, "pytool_stub", "#!"+absentPython+"\nprint('hi')\n")
		stub := writeRawFile(t, dir, "python-stub", "")
		out := explainUnusablePython(script, stub, "", enoentExec(script))
		msg := out.Error()
		if !strings.Contains(msg, "did not report a version") {
			t.Errorf("message should say the interpreter reported no version; got %q", msg)
		}
	})

	t.Run("a set override is blamed instead of suggesting one", func(t *testing.T) {
		t.Setenv(strPythonOverrideEnv, "/somewhere/python")
		script := writeRawFile(t, dir, "pytool_override", "#!"+absentPython+"\nprint('hi')\n")
		out := explainUnusablePython(script, "/somewhere/python", "3.8", enoentExec(script))
		msg := out.Error()
		if !strings.Contains(msg, strPythonOverrideEnv+" is set") {
			t.Errorf("message should say the override is set but unusable; got %q", msg)
		}
	})

	t.Run("env python shebang with a below-floor python is explained", func(t *testing.T) {
		script := writeRawFile(t, dir, "pytool_env_old", "#!/usr/bin/env python3\nprint('hi')\n")
		out := explainUnusablePython(script, "/usr/bin/python3", "3.9", enoentExec(script))
		msg := out.Error()
		if !strings.Contains(msg, "3.9") || !strings.Contains(msg, strPythonFloor) {
			t.Errorf("env shebang runs on the resolved python, so name 3.9 and the %s floor; got %q", strPythonFloor, msg)
		}
	})

	t.Run("env python shebang with a python at the floor is left unchanged", func(t *testing.T) {
		script := writeRawFile(t, dir, "pytool_env_ok", "#!/usr/bin/env python3\nprint('hi')\n")
		in := enoentExec(script)
		if out := explainUnusablePython(script, "python3", strPythonFloor, in); out != in {
			t.Errorf("python meets the floor; want err unchanged, got %q", out)
		}
	})

	t.Run("windows .py without any shebang is explained", func(t *testing.T) {
		fakeWindowsGOOS(t)
		script := writeRawFile(t, dir, "flat_tool.py", "print('hi')\n")
		out := explainUnusablePython(script, absentPython, "", enoentExec(script))
		if !strings.Contains(out.Error(), "requires Python "+strPythonFloor) {
			t.Errorf("flat windows .py should get the install hint; got %q", out.Error())
		}
	})

	t.Run("script with a live python shebang is left unchanged", func(t *testing.T) {
		interp := writeRawFile(t, dir, "python3.12", "#!/bin/sh\n")
		script := writeRawFile(t, dir, "pytool_venv", "#!"+interp+"\nprint('hi')\n")
		in := enoentExec(script)
		if out := explainUnusablePython(script, "python3", "", in); out != in {
			t.Errorf("live venv shebang does not use the resolved interpreter; got %q", out)
		}
	})

	t.Run("non-python script is left unchanged", func(t *testing.T) {
		script := writeRawFile(t, dir, "shtool", "#!/bin/sh\necho hi\n")
		in := enoentExec(script)
		if out := explainUnusablePython(script, absentPython, "", in); out != in {
			t.Errorf("non-python shebang must pass through; got %q", out)
		}
	})
}

func TestExplainMissingInterpreter(t *testing.T) {
	dir := t.TempDir()

	t.Run("existing script with missing python shebang is explained", func(t *testing.T) {
		script := writeRawFile(t, dir, "tool_a", "#!/nonexistent/opt/python@3.14/bin/python3.14\nx\n")
		out := explainMissingInterpreter(script, enoentExec(script))
		msg := out.Error()
		if !strings.Contains(msg, "/nonexistent/opt/python@3.14/bin/python3.14") {
			t.Errorf("message should name the missing interpreter; got %q", msg)
		}
		if !strings.Contains(msg, "sam toolset package") {
			t.Errorf("message should point at the rebuild command; got %q", msg)
		}
	})

	t.Run("missing executable is left unchanged", func(t *testing.T) {
		ghost := writeRawFile(t, dir, "ghost-ref", "x")
		_ = os.Remove(ghost)
		in := enoentExec(ghost)
		if out := explainMissingInterpreter(ghost, in); out != in {
			t.Errorf("a missing executable is a different problem; want err unchanged, got %q", out)
		}
	})

	t.Run("script without a shebang is left unchanged", func(t *testing.T) {
		script := writeRawFile(t, dir, "tool_b", "no shebang here\n")
		in := enoentExec(script)
		if out := explainMissingInterpreter(script, in); out != in {
			t.Errorf("no shebang means no interpreter diagnosis; want err unchanged, got %q", out)
		}
	})

	t.Run("non-ENOENT error is left unchanged", func(t *testing.T) {
		script := writeRawFile(t, dir, "tool_c", "#!/nonexistent/python3\nx\n")
		in := errors.New("running x --schema: exit status 1")
		if out := explainMissingInterpreter(script, in); out != in {
			t.Errorf("non-ENOENT error must pass through; got %q", out)
		}
	})
}

func TestExplainNativeABIMismatch(t *testing.T) {
	t.Run("compiled-extension import failure is explained", func(t *testing.T) {
		in := errors.New("running tool --schema: exit status 1 (stderr: Traceback ... | " +
			"ModuleNotFoundError: No module named 'pydantic_core._pydantic_core')")
		out := explainNativeABIMismatch(in)
		if out == in {
			t.Fatal("expected the native-ABI import failure to be explained")
		}
		msg := out.Error()
		if !strings.Contains(msg, "SAM_TOOL_PYTHON_VERSION") || !strings.Contains(msg, "native") {
			t.Errorf("message should name the rebuild env var and the native cause; got %q", msg)
		}
	})

	t.Run("missing pure-Python dep is left unchanged", func(t *testing.T) {
		in := errors.New("running tool --schema: exit status 1 (stderr: " +
			"ModuleNotFoundError: No module named 'requests')")
		if out := explainNativeABIMismatch(in); out != in {
			t.Errorf("a plain missing dep is not a native-ABI mismatch; want unchanged, got %q", out)
		}
	})

	t.Run("nil passes through", func(t *testing.T) {
		if out := explainNativeABIMismatch(nil); out != nil {
			t.Errorf("nil in, nil out; got %v", out)
		}
	})
}

func TestExplainDiscoveryError(t *testing.T) {
	dir := t.TempDir()

	t.Run("exec format error wins", func(t *testing.T) {
		bin := writeRawFile(t, dir, "elf", "\x7fELF\x02\x01\x01\x00")
		in := fmt.Errorf("running %s --schema: %w", bin, errors.New("exec format error"))
		out := explainDiscoveryError(bin, in)
		if !strings.Contains(out.Error(), "cannot run on this host") {
			t.Errorf("expected the OS/arch diagnosis; got %q", out)
		}
	})

	t.Run("native ABI mismatch", func(t *testing.T) {
		in := errors.New("running tool --schema: exit status 1 (stderr: " +
			"No module named 'pydantic_core._pydantic_core')")
		out := explainDiscoveryError("tool", in)
		if !strings.Contains(out.Error(), "SAM_TOOL_PYTHON_VERSION") {
			t.Errorf("expected the native-ABI diagnosis; got %q", out)
		}
	})

	t.Run("unrecognized error is unchanged", func(t *testing.T) {
		in := errors.New("running tool --schema: exit status 1 (stderr: boom)")
		if out := explainDiscoveryError("tool", in); out != in {
			t.Errorf("an unrecognized failure must pass through; got %q", out)
		}
	})

	t.Run("nil passes through", func(t *testing.T) {
		if out := explainDiscoveryError("tool", nil); out != nil {
			t.Errorf("nil in, nil out; got %v", out)
		}
	})
}
