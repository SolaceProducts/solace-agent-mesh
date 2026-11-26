// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const strPythonFloor = "3.10"

const strPythonOverrideEnv = "SAM_STR_PYTHON"

const maxProbePythonMinor = 18

const windowsPythonExe = "python.exe"

var resolveSTRPython = sync.OnceValues(func() (path, version string) {
	return resolvePython(strPythonPlan, probePythonVersion)
})

func resolvePython(plan func() (ambient string, scan func() []string), probe func(string) string) (path, version string) {
	ambient, scan := plan()
	return selectPythonInterpreter(os.Getenv(strPythonOverrideEnv), ambient, scan, strPythonFloor, probe)
}

func strPythonPlan() (ambient string, scan func() []string) {
	switch runtime.GOOS {
	case "windows":
		return "python", windowsPythonCandidates
	case "darwin":
		return "python3", func() []string { return pythonScanList(macOSPythonDirs()) }
	default:
		return "python3", func() []string { return pythonScanList(linuxPythonDirs()) }
	}
}

func macOSPythonDirs() []string {
	dirs := []string{"/opt/homebrew/bin", "/usr/local/bin"}
	frameworkBins, _ := filepath.Glob("/Library/Frameworks/Python.framework/Versions/*/bin")
	return append(dirs, frameworkBins...)
}

func linuxPythonDirs() []string {
	dirs := []string{"/usr/local/bin", "/usr/bin"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"))
	}
	return dirs
}

func windowsPythonCandidates() []string {
	out := pyLauncherPythons()
	out = append(out, windowsInstallDirPythons()...)
	return append(out, "python3")
}

func pyLauncherPythons() []string {
	launcher := pyLauncherPath()
	if launcher == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), pythonProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, launcher, "--list-paths")
	hideConsoleWindow(cmd)
	cmd.WaitDelay = time.Second
	out, err := childCombinedOutput(cmd)
	if err != nil {
		return nil
	}
	return parsePyListPaths(string(out))
}

func pyLauncherPath() string {
	for _, p := range []string{
		filepath.Join(os.Getenv("SystemRoot"), "py.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Python", "Launcher", "py.exe"),
	} {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

var pyLauncherPathRe = regexp.MustCompile(`(?i)([a-z]:[\\/]|\\\\).*?\.exe`)

func parsePyListPaths(out string) []string {
	var paths []string
	for line := range strings.SplitSeq(out, "\n") {
		if p := pyLauncherPathRe.FindString(line); p != "" {
			paths = append(paths, strings.TrimSpace(p))
		}
	}
	return paths
}

func windowsInstallDirPythons() []string {
	var out []string
	for _, pattern := range windowsPythonGlobs() {
		matches, _ := filepath.Glob(pattern)
		out = append(out, matches...)
	}
	return out
}

func windowsPythonGlobs() []string {
	var patterns []string
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		patterns = append(patterns,
			filepath.Join(base, "Programs", "Python", "Python3*", windowsPythonExe),
			filepath.Join(base, "Microsoft", "WindowsApps", "PythonSoftwareFoundation.Python.3.*", windowsPythonExe),
		)
	}
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
		if base := os.Getenv(env); base != "" {
			patterns = append(patterns, filepath.Join(base, "Python3*", windowsPythonExe))
		}
	}
	return append(patterns, `C:\Python3*\`+windowsPythonExe)
}

func pythonScanList(dirs []string) []string {
	_, floorMinor, _ := pythonMajorMinorInts(strPythonFloor)
	var out []string
	for minor := maxProbePythonMinor; minor >= floorMinor; minor-- {
		name := "python3." + strconv.Itoa(minor)
		out = append(out, name)
		for _, d := range dirs {
			out = append(out, filepath.Join(d, name))
		}
	}
	for _, d := range dirs {
		out = append(out, filepath.Join(d, "python3"))
	}
	return out
}

func selectPythonInterpreter(override, ambient string, scan func() []string, floor string, probe func(string) string) (path, version string) {
	if override != "" {
		if v := probe(override); pythonAtLeast(v, floor) {
			return override, v
		}
	}
	ambientVer := probe(ambient)
	if pythonAtLeast(ambientVer, floor) {
		return ambient, ambientVer
	}
	if bestPath, bestVer := firstAtLeast(scan(), floor, probe); bestPath != "" {
		return bestPath, bestVer
	}
	return ambient, ambientVer
}

func firstAtLeast(candidates []string, floor string, probe func(string) string) (path, version string) {
	for _, c := range candidates {
		if v := probe(c); pythonAtLeast(v, floor) {
			return c, v
		}
	}
	return "", ""
}

func pythonAtLeast(v, floor string) bool {
	vMaj, vMin, ok := pythonMajorMinorInts(v)
	if !ok {
		return false
	}
	fMaj, fMin, ok := pythonMajorMinorInts(floor)
	if !ok {
		return false
	}
	if vMaj != fMaj {
		return vMaj > fMaj
	}
	return vMin >= fMin
}

func pythonMajorMinorInts(v string) (major, minor int, ok bool) {
	majStr, rest, found := strings.Cut(v, ".")
	if !found {
		return 0, 0, false
	}
	minStr, _, _ := strings.Cut(rest, ".")
	major, majErr := strconv.Atoi(majStr)
	minor, minErr := strconv.Atoi(minStr)
	if majErr != nil || minErr != nil {
		return 0, 0, false
	}
	return major, minor, true
}
