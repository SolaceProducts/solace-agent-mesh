// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/execbit"
)

func SetupToolDir(ctx context.Context, runner *SandboxRunner, toolDir string, logger *slog.Logger) error {
	return SetupToolDirWithOptions(ctx, runner, toolDir, false, logger)
}

func SetupToolDirWithOptions(ctx context.Context, runner *SandboxRunner, toolDir string, ours bool, logger *slog.Logger) error {
	if err := extractZips(toolDir, ours, logger); err != nil {
		return fmt.Errorf("extracting zips in %s: %w", toolDir, err)
	}
	if err := runSetupScript(ctx, runner, toolDir, ours, logger); err != nil {
		return fmt.Errorf("running setup.sh in %s: %w", toolDir, err)
	}

	if findWheels(toolDir) != nil {
		return fmt.Errorf(
			"toolset ships .whl wheels — this packaging style is no longer supported. " +
				"SAM follows the Lambda Layer model: ship dependencies pre-extracted under " +
				"a python/ directory at the package root, with native extensions built for " +
				"the runtime OS/arch/Python. Re-package with: " +
				"`pip install --target <pkg>/python --platform <plat> --python-version <v> --only-binary=:all: <reqs>` " +
				"and then `zip -r <pkg>.zip <pkg>/`.")
	}
	if _, err := os.Stat(filepath.Join(toolDir, ".venv")); err == nil {
		return fmt.Errorf(
			"toolset ships a pre-installed .venv/ — this packaging style is no longer supported " +
				"(venvs embed absolute paths and a Python interpreter, breaking when extracted into a " +
				"different filesystem location). SAM follows the Lambda Layer model: ship dependencies " +
				"pre-extracted under a python/ directory at the package root. Re-package with: " +
				"`pip install --target <pkg>/python --platform <plat> --python-version <v> --only-binary=:all: <reqs>` " +
				"and then `zip -r <pkg>.zip <pkg>/`.")
	}

	precompilePython(ctx, runner, toolDir, logger)
	return nil
}

func hasPythonLayer(toolDir string) bool {
	pyDir := filepath.Join(toolDir, "python")
	entries, err := os.ReadDir(pyDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() || strings.HasSuffix(name, ".py") {
			return true
		}
	}
	return false
}

func PythonPathFor(toolDir string) string {
	if hasPythonLayer(toolDir) {
		return filepath.Join(toolDir, "python") + string(os.PathListSeparator) + toolDir
	}
	return toolDir
}

const (
	setupScriptName = "setup.sh"
	setupMarkerName = ".sam-setup-done"
)

func readFileIn(dir, name string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.ReadFile(name)
}

func writeFileIn(dir, name string, data []byte, perm os.FileMode) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return root.WriteFile(name, data, perm)
}

func runSetupScript(ctx context.Context, runner *SandboxRunner, toolDir string, ours bool, logger *slog.Logger) error {
	scriptData, err := readFileIn(toolDir, setupScriptName)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading setup.sh: %w", err)
	}
	scriptHash := fmt.Sprintf("%x", sha256.Sum256(scriptData))
	if marker, markerErr := readFileIn(toolDir, setupMarkerName); markerErr == nil && strings.TrimSpace(string(marker)) == scriptHash {
		logger.Debug("setup.sh already run (marker matches)", dirLogAttrs(ours, toolDir)...)
		return nil
	}

	bash, err := exec.LookPath("bash")
	if err != nil {
		return fmt.Errorf("setup.sh needs bash: %w", err)
	}
	logger.Info("running setup.sh", dirLogAttrs(ours, toolDir)...)
	if err := runner.runInstallStep(ctx, installStep{
		name: setupScriptName, toolDir: toolDir, executable: bash,
		args:            []string{"-c", `cd "$1" && exec bash ./setup.sh`, setupScriptName, toolDir},
		writableToolDir: true, stdout: os.Stderr, stderr: os.Stderr,
	}); err != nil {
		return fmt.Errorf("setup.sh failed: %w", err)
	}

	if err := writeFileIn(toolDir, setupMarkerName, []byte(scriptHash+"\n"), 0o600); err != nil {
		logger.Warn("failed to write setup marker", append(failureLogAttrs(ours, err), dirLogAttrs(ours, toolDir)...)...)
	}
	return nil
}

func extractZips(toolDir string, ours bool, logger *slog.Logger) error {
	entries, freshZips, err := listFreshZips(toolDir)
	if err != nil || len(freshZips) == 0 {
		return err
	}

	root, err := os.OpenRoot(toolDir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()

	removeExtracted(root, toolDir, entries, ours, logger)

	for _, name := range freshZips {
		zipPath := filepath.Join(toolDir, name)
		logger.Info("extracting zip", dirLogAttrs(ours, zipPath, "path")...)

		if err := extractZipFile(zipPath, toolDir); err != nil {
			return fmt.Errorf("extracting %s: %w", zipPath, err)
		}

		if err := root.Rename(name, name+".extracted"); err != nil {
			logger.Warn("failed to rename extracted zip", append(failureLogAttrs(ours, err), dirLogAttrs(ours, zipPath, "path")...)...)
		}
	}

	return nil
}

func listFreshZips(toolDir string) ([]os.DirEntry, []string, error) {
	entries, err := os.ReadDir(toolDir)
	if err != nil {
		return nil, nil, err
	}
	var freshZips []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".zip") {
			freshZips = append(freshZips, e.Name())
		}
	}
	return entries, freshZips, nil
}

func removeExtracted(root *os.Root, toolDir string, entries []os.DirEntry, ours bool, logger *slog.Logger) {
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".zip.extracted") {
			continue
		}
		if err := root.RemoveAll(name); err != nil {
			logger.Warn("failed to remove stale extracted entry",
				append(failureLogAttrs(ours, err), dirLogAttrs(ours, filepath.Join(toolDir, name), "path")...)...)
		}
	}
}

const maxZipManifestBytes = 1 << 20

func freshZipClass(toolDir string) (class string, found bool, err error) {
	_, freshZips, err := listFreshZips(toolDir)
	if err != nil || len(freshZips) == 0 {
		return "", false, err
	}
	data, found, err := readZipManifest(filepath.Join(toolDir, freshZips[0]))
	if err != nil || !found {
		return "", false, err
	}
	var m packageManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return "", false, fmt.Errorf("parsing %s in %s: %w", manifestFileName, freshZips[0], err)
	}
	return strings.TrimSpace(m.ExecutionClass), true, nil
}

func readZipManifest(zipPath string) ([]byte, bool, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = zr.Close() }()
	for _, f := range zr.File {
		if f.Name != manifestFileName {
			continue
		}
		if f.UncompressedSize64 > maxZipManifestBytes {
			return nil, false, fmt.Errorf("%s in %s is larger than %d bytes", manifestFileName, zipPath, maxZipManifestBytes)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, false, err
		}
		data, err := io.ReadAll(io.LimitReader(rc, maxZipManifestBytes+1))
		_ = rc.Close()
		if err != nil {
			return nil, false, err
		}
		return data, true, nil
	}
	return nil, false, nil
}

func keepManifestOnly(toolDir string, logger *slog.Logger) error {
	entries, freshZips, err := listFreshZips(toolDir)
	if err != nil || len(freshZips) == 0 {
		return err
	}
	data, found, err := readZipManifest(filepath.Join(toolDir, freshZips[0]))
	if err != nil || !found {
		return err
	}
	root, err := os.OpenRoot(toolDir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	removeExtracted(root, toolDir, entries, false, logger)
	return root.WriteFile(manifestFileName, data, 0o644)
}

func extractedPerm(f *zip.File, staged *os.File) os.FileMode {
	if f.Mode().Perm()&0o111 != 0 || execbit.LooksRunnableFile(staged) {
		return 0o755
	}
	return 0o644
}

const maxDecompressedSize = 1 << 30

func extractZipFile(zipPath, destDir string) error {
	dir, err := os.OpenRoot(filepath.Dir(zipPath))
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	f, err := dir.Open(filepath.Base(zipPath))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		return err
	}
	return extractZip(zr, destDir, nil)
}

func hiddenPartialExtractRel(rel string) string {
	return filepath.Join(filepath.Dir(rel), "."+filepath.Base(rel)+".tmp")
}

func extractZip(zr *zip.Reader, destDir string, skip func(name string) bool) error {
	root, err := os.OpenRoot(destDir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()

	var totalSize uint64
	for _, f := range zr.File {
		target := filepath.Join(destDir, f.Name)
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path in zip: %s", f.Name)
		}
		if skip != nil && skip(f.Name) {
			continue
		}
		rel, _ := filepath.Rel(destDir, target)

		if f.FileInfo().IsDir() {
			if err := root.MkdirAll(rel, 0o755); err != nil {
				return err
			}
			continue
		}

		totalSize += f.UncompressedSize64
		if totalSize > maxDecompressedSize {
			return fmt.Errorf("zip decompressed size exceeds limit (%d bytes)", maxDecompressedSize)
		}

		if err := root.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
			return err
		}

		tmpRel := hiddenPartialExtractRel(rel)
		tmpTarget := filepath.Join(destDir, tmpRel)
		outFile, err := root.OpenFile(tmpRel, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			_ = outFile.Close()
			_ = root.Remove(tmpRel)
			return err
		}

		written, err := io.Copy(outFile, io.LimitReader(rc, int64(maxDecompressedSize-totalSize)+1))
		_ = rc.Close()
		if err != nil {
			_ = outFile.Close()
			_ = root.Remove(tmpRel)
			return err
		}

		if chmodErr := outFile.Chmod(extractedPerm(f, outFile)); chmodErr != nil {
			_ = outFile.Close()
			_ = root.Remove(tmpRel)
			return fmt.Errorf("chmod %q: %w", tmpTarget, chmodErr)
		}
		if closeErr := outFile.Close(); closeErr != nil {
			_ = root.Remove(tmpRel)
			return closeErr
		}
		if renameErr := root.Rename(tmpRel, rel); renameErr != nil {
			_ = root.Remove(tmpRel)
			return fmt.Errorf("rename %q to %q: %w", tmpTarget, target, renameErr)
		}
		actualDelta := uint64(written)
		if actualDelta > f.UncompressedSize64 {
			totalSize += actualDelta - f.UncompressedSize64
		}
		if totalSize > maxDecompressedSize {
			return fmt.Errorf("zip decompressed size exceeds limit (%d bytes)", maxDecompressedSize)
		}
	}

	return nil
}

func findWheels(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var wheels []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".whl") {
			wheels = append(wheels, filepath.Join(dir, e.Name()))
		}
	}
	slices.Sort(wheels)
	return wheels
}
