// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	instanceDirPrefix = "str-"
	instanceLockName  = ".lock"

	staleInstanceGrace = time.Minute

	workDirPrefix = "w-"

	workDirHome = "home"
	workDirTmp  = "tmp"
)

var errFileLockUnsupported = errors.New("file locks are not supported on this platform")

func (r *SandboxRunner) instanceWorkDir() (string, error) {
	r.instanceMu.Lock()
	defer r.instanceMu.Unlock()
	if r.instanceDir == "" {
		if err := os.MkdirAll(r.cfg.WorkBaseDir, 0o700); err != nil {
			return "", err
		}
		dir, err := os.MkdirTemp(r.cfg.WorkBaseDir, instanceDirPrefix+"*")
		if err != nil {
			return "", err
		}
		r.instanceDir = dir
		r.log().Info("claimed STR instance dir", "instanceDirName", filepath.Base(dir))
	}
	if err := r.ensureInstanceLockLocked(); err != nil {
		return "", err
	}
	return r.instanceDir, nil
}

func (r *SandboxRunner) ensureInstanceLockLocked() error {
	lockPath := filepath.Join(r.instanceDir, instanceLockName)
	if r.instanceLock != nil && sameFileAtPath(r.instanceLock, lockPath) {
		return nil
	}
	if r.instanceLock != nil {
		_ = r.instanceLock.Close()
		r.instanceLock = nil
	}
	if err := os.MkdirAll(r.instanceDir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := tryLockFile(lock); err != nil && !errors.Is(err, errFileLockUnsupported) {
		r.log().Warn("instance dir lock unavailable; dir stays unlocked",
			"instanceDirName", filepath.Base(r.instanceDir), "error", err)
	}
	r.instanceLock = lock
	return nil
}

func sameFileAtPath(f *os.File, path string) bool {
	held, err := f.Stat()
	if err != nil {
		return false
	}
	onDisk, err := os.Stat(path)
	if err != nil {
		return false
	}
	return os.SameFile(held, onDisk)
}

func (r *SandboxRunner) releaseInstanceDir() {
	r.instanceMu.Lock()
	defer r.instanceMu.Unlock()
	if r.instanceLock == nil {
		return
	}
	_ = r.instanceLock.Close()
	r.instanceLock = nil
	dir := r.instanceDir
	r.instanceDir = ""
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	if len(entries) == 1 && entries[0].Name() == instanceLockName {
		_ = os.Remove(filepath.Join(dir, instanceLockName))
		_ = os.Remove(dir)
		return
	}
	r.log().Warn("instance dir not empty at shutdown; the next STR start removes it",
		"instanceDirName", filepath.Base(dir), "leftoverEntries", len(entries)-1)
}

func removeInstanceDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == instanceLockName {
			continue
		}
		if err := forceRemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	if err := os.Remove(filepath.Join(dir, instanceLockName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.Remove(dir)
}

func forceRemoveAll(path string) error {
	err := os.RemoveAll(path)
	if err == nil || !errors.Is(err, fs.ErrPermission) {
		return err
	}
	root, openErr := os.OpenRoot(path)
	if openErr != nil {
		return err
	}
	_ = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr == nil && d.IsDir() {
			_ = root.Chmod(p, 0o700)
		}
		return nil
	})
	_ = root.Close()
	return os.RemoveAll(path)
}

func (r *SandboxRunner) sweepStaleInstanceDirs(ctx context.Context) {
	logger := r.log()
	own, err := r.instanceWorkDir()
	if err != nil {
		logger.Warn("work dir sweep skipped: instance dir unavailable", "error", err)
		return
	}
	entries, err := os.ReadDir(r.cfg.WorkBaseDir)
	if err != nil {
		logger.Warn("work dir sweep skipped: base dir unreadable", "error", err)
		return
	}
	for _, e := range entries {
		if ctx.Err() != nil {
			return
		}
		if !e.IsDir() || !isInstanceDirName(e.Name()) {
			continue
		}
		dir := filepath.Join(r.cfg.WorkBaseDir, e.Name())
		if dir == own {
			continue
		}
		lockAge, stale := instanceIsStale(dir)
		if !stale {
			continue
		}
		if err := removeInstanceDir(dir); err != nil {
			logger.Warn("stale instance dir removal failed", "instanceDirName", e.Name(), "error", err)
			continue
		}
		logger.Info("removed stale STR instance dir", "instanceDirName", e.Name(), "lockAge_s", int(lockAge.Seconds()))
	}
}

func isInstanceDirName(name string) bool {
	suffix, ok := strings.CutPrefix(name, instanceDirPrefix)
	if !ok || suffix == "" {
		return false
	}
	for _, c := range suffix {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func instanceIsStale(dir string) (time.Duration, bool) {
	lock, err := os.OpenFile(filepath.Join(dir, instanceLockName), os.O_RDWR, 0)
	if err != nil {
		return 0, false
	}
	defer func() { _ = lock.Close() }()
	info, err := lock.Stat()
	if err != nil {
		return 0, false
	}
	age := time.Since(info.ModTime())
	if age < staleInstanceGrace {
		return age, false
	}
	return age, tryLockFile(lock) == nil
}

func scratchEnv(workDir string) (home, tmp string) {
	return filepath.Join(workDir, workDirHome), filepath.Join(workDir, workDirTmp)
}

func (r *SandboxRunner) log() *slog.Logger {
	if r.logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return r.logger
}
