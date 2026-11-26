// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	freshProcAuto   = "auto"
	freshProcAlways = "always"
	freshProcNever  = "never"
)

const freshProcProbeTimeout = 5 * time.Second

type execProbeFunc func(ctx context.Context, name string, args ...string) (exitCode int, stderr string, err error)

func decideFreshProc(exitCode int, stderr string) bool {
	if exitCode != 0 {
		return false
	}
	return !freshProcRejected(stderr)
}

func freshProcRejected(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "can't mount proc") ||
		strings.Contains(s, "mount_too_revealing") ||
		strings.Contains(s, "operation not permitted")
}

func resolveFreshProc(cfg SandboxConfig, probed bool) bool {
	switch cfg.FreshProc {
	case freshProcAlways:
		return true
	case freshProcNever:
		return false
	default:
		return probed
	}
}

func validateFreshProc(v string) error {
	switch v {
	case "", freshProcAuto, freshProcAlways, freshProcNever:
		return nil
	default:
		return fmt.Errorf("invalid sandbox.fresh_proc %q: must be auto, always, or never", v)
	}
}

func probeFreshProc(ctx context.Context, bwrapBin string, run execProbeFunc) (keep, probeFailed bool, exitCode int, stderr string) {
	args := []string{"--die-with-parent", "--unshare-pid", bwrapFlagROBind, "/", "/", "--proc", "/proc", "--", "/bin/true"}
	exitCode, stderr, err := run(ctx, bwrapBin, args...)
	if err != nil {
		return false, true, exitCode, strings.TrimSpace(stderr + " " + err.Error())
	}
	keep = decideFreshProc(exitCode, stderr)
	probeFailed = !keep && exitCode != 0 && !freshProcRejected(stderr)
	return keep, probeFailed, exitCode, stderr
}

func realProbeExec(ctx context.Context, name string, args ...string) (int, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := runChild(cmd)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return -1, stderr.String(), fmt.Errorf("fresh-proc probe context ended: %w", ctxErr)
	}
	if err == nil {
		return 0, stderr.String(), nil
	}
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return ee.ExitCode(), stderr.String(), nil
	}
	return -1, stderr.String(), err
}

func (r *SandboxRunner) resolveFreshProcSupport(ctx context.Context, run execProbeFunc) {
	if r.cfg.Mode != "bwrap" {
		return
	}
	r.warnIfBwrapSetuid()

	if r.cfg.FreshProc == freshProcAlways || r.cfg.FreshProc == freshProcNever {
		r.freshProc = resolveFreshProc(r.cfg, false)
		if r.logger != nil {
			r.logger.Info("STR fresh-proc resolved from config; probe skipped",
				"freshProcMode", r.cfg.FreshProc, "freshProc", r.freshProc)
		}
		return
	}

	if run == nil {
		run = realProbeExec
	}
	probeCtx, cancel := context.WithTimeout(ctx, freshProcProbeTimeout)
	defer cancel()
	keep, probeFailed, exitCode, stderr := probeFreshProc(probeCtx, r.cfg.BwrapBin, run)
	r.freshProc = resolveFreshProc(r.cfg, keep)
	if r.logger != nil {
		logFn, msg := r.logger.Info, "STR fresh-proc probe complete"
		if probeFailed {
			logFn, msg = r.logger.Warn, "STR fresh-proc probe failed to run; dropping --proc conservatively (not a confirmed kernel rejection)"
		}
		logFn(msg,
			"freshProcMode", r.cfg.FreshProc, "probed", keep, "freshProc", r.freshProc,
			"exitCode", exitCode, "rejected", freshProcRejected(stderr), "probeFailed", probeFailed,
			"stderr", truncateProbeStderr(stderr))
	}
}

func (r *SandboxRunner) warnIfBwrapSetuid() bool {
	info, err := os.Stat(r.cfg.BwrapBin)
	if err != nil {
		if r.logger != nil {
			r.logger.Warn("could not stat bwrap binary to check setuid bit; ensure sandbox.bwrap_bin points to a valid non-setuid bubblewrap",
				"bwrapBin", r.cfg.BwrapBin, "error", err)
		}
		return false
	}
	setuid := info.Mode()&os.ModeSetuid != 0
	if setuid && r.logger != nil {
		r.logger.Warn("bwrap binary is setuid; per-tool user-namespace isolation is weakened (cross-tool /proc secret reads may be possible) — install a non-setuid bubblewrap",
			"bwrapBin", r.cfg.BwrapBin)
	}
	return setuid
}

func truncateProbeStderr(s string) string {
	const max = 256
	s = strings.TrimSpace(s)
	if len(s) > max {
		s = s[:max]
	}
	return strings.ToValidUTF8(s, "")
}
