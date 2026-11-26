// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build linux

package str

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

var (
	trackedMu       sync.Mutex
	trackedChildren = map[int]struct{}{}
	startsInFlight  atomic.Int32
)

func beginChildStart() { startsInFlight.Add(1) }

func abortChildStart() { startsInFlight.Add(-1) }

func trackChild(pid int) {
	trackedMu.Lock()
	trackedChildren[pid] = struct{}{}
	trackedMu.Unlock()
	startsInFlight.Add(-1)
}

func untrackChild(pid int) {
	trackedMu.Lock()
	delete(trackedChildren, pid)
	trackedMu.Unlock()
}

func isTrackedChild(pid int) bool {
	trackedMu.Lock()
	defer trackedMu.Unlock()
	_, ok := trackedChildren[pid]
	return ok
}

type siginfoHead struct {
	_      [3]int32
	fields struct {
		_ int32
		_ uintptr
	}
}

const siginfoPidOffset = unsafe.Offsetof(siginfoHead{}.fields)

var peekFailureLogged atomic.Bool

func peekZombie() (pid int, err error) {
	var si unix.Siginfo
	for {
		err = unix.Waitid(unix.P_ALL, 0, &si, unix.WEXITED|unix.WNOHANG|unix.WNOWAIT, nil)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return 0, err
		}
		return int(*(*int32)(unsafe.Pointer(uintptr(unsafe.Pointer(&si)) + siginfoPidOffset))), nil
	}
}

func drainOrphans(logger *slog.Logger) {
	for {
		pid, err := peekZombie()
		if err != nil {
			if err != unix.ECHILD && logger != nil && peekFailureLogged.CompareAndSwap(false, true) {
				logger.Warn("global reaper cannot peek exited children; orphaned processes will not be collected", "error", err)
			}
			return
		}
		if pid <= 0 {
			return
		}
		if startsInFlight.Load() > 0 || isTrackedChild(pid) {
			return
		}
		var ws syscall.WaitStatus
		reaped, err := syscall.Wait4(pid, &ws, syscall.WNOHANG, nil)
		if reaped <= 0 {
			if err == syscall.EINTR {
				continue
			}
			return
		}
		if logger != nil {
			logger.Debug("global reaper collected orphan", "pid", reaped, "exited", ws.Exited(), "exitCode", ws.ExitStatus())
		}
	}
}

func StartGlobalReaper(ctx context.Context, logger *slog.Logger) {
	sigCh := make(chan os.Signal, 8)
	signal.Notify(sigCh, syscall.SIGCHLD)

	go func() {
		defer signal.Stop(sigCh)

		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-sigCh:
				drainOrphans(logger)
			case <-ticker.C:
				drainOrphans(logger)
			}
		}
	}()
}
