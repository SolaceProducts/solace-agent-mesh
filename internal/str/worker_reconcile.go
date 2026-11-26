// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
)

type reconcileReason string

const (
	reconcileReasonBoot      reconcileReason = "boot"
	reconcileReasonPush      reconcileReason = "push"
	reconcileReasonReconnect reconcileReason = "reconnect"
	reconcileReasonPeriodic  reconcileReason = "periodic"
)

func (w *Worker) signalReconcile(reason reconcileReason) {
	select {
	case w.reconcileTrigger <- reason:
	default:
	}
}

func (w *Worker) reconcileRunLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case reason := <-w.reconcileTrigger:
			w.runReconcile(ctx, reason)
		}
	}
}

func (w *Worker) runReconcile(ctx context.Context, reason reconcileReason) {
	start := time.Now()

	pkgChanged := w.reconcilePackages(ctx, reason)
	skillChanged := w.reconcileSkills(ctx, reason)

	changed := pkgChanged || skillChanged
	result := "nochange"
	if changed {
		result = "changed"
	}

	w.metrics.recordReconcileRun(time.Since(start), string(reason), result)

	if changed && reason != reconcileReasonPush && reason != reconcileReasonBoot {
		w.logger.Warn("reconcile drift: non-push reconcile found changes; push event likely dropped",
			"reason", reason)
		w.metrics.recordReconcileDrift(string(reason))
	}
}

func (w *Worker) reconcilePackages(ctx context.Context, reason reconcileReason) bool {
	if w.packageProcessor == nil {
		return false
	}

	if !objectstore.IsNoop(w.packageStore) {
		if err := w.packageStore.FetchAll(ctx); err != nil {
			w.logger.Warn("reconcile: package FetchAll failed", "reason", reason, "error", err)
			if reason == reconcileReasonBoot {
				w.setPackageFetchAllPending(true)
			}
		} else {
			w.setPackageFetchAllPending(false)
		}
		if reason != reconcileReasonBoot {
			w.pruneLocalPackages(ctx, reason)
		}
	}

	return w.packageScanOnce(ctx)
}

func (w *Worker) pruneLocalPackages(ctx context.Context, reason reconcileReason) {
	names, err := w.packageStore.RemoteNames(ctx)
	if err != nil {
		w.logger.Warn("reconcile: package RemoteNames failed; skipping prune",
			"reason", reason, "error", err)
		return
	}

	remoteSet := strSet(names)
	dir := w.packageProcessor.toolsDir

	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		return
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if remoteSet[name] {
			continue
		}
		if !isPackageDir(filepath.Join(dir, name)) {
			continue
		}
		if rmErr := w.packageProcessor.RemovePackageDir(name); rmErr != nil {
			w.logger.Warn("reconcile: failed to prune package dir",
				"pkg", name, "error", rmErr)
		} else {
			w.logger.Info("reconcile: pruned stale package directory", "pkg", name, "reason", reason)
		}
	}
}

func (w *Worker) reconcileSkills(ctx context.Context, reason reconcileReason) bool {
	if w.skillRegistry == nil {
		return false
	}

	if !objectstore.IsNoop(w.skillStore) {
		if err := w.skillStore.FetchAll(ctx); err != nil {
			w.logger.Warn("reconcile: skill FetchAll failed", "reason", reason, "error", err)
			if reason == reconcileReasonBoot {
				w.setSkillFetchAllPending(true)
			}
		} else {
			w.setSkillFetchAllPending(false)
		}
		if reason != reconcileReasonBoot {
			w.pruneLocalSkills(ctx, reason)
		}
	}

	return w.reconcileSkillChanges(ctx)
}

func (w *Worker) pruneLocalSkills(ctx context.Context, reason reconcileReason) {
	if w.cfg.SkillsDir == "" {
		return
	}

	names, err := w.skillStore.RemoteNames(ctx)
	if err != nil {
		w.logger.Warn("reconcile: skill RemoteNames failed; skipping prune",
			"reason", reason, "error", err)
		return
	}

	remoteSet := strSet(names)
	entries, readErr := os.ReadDir(w.cfg.SkillsDir)
	if readErr != nil {
		return
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if remoteSet[name] {
			continue
		}
		extracted := filepath.Join(w.cfg.SkillsDir, name)
		if !isExtractedSkillDir(extracted) {
			continue
		}
		if rmErr := os.RemoveAll(extracted); rmErr != nil && !os.IsNotExist(rmErr) {
			w.logger.Warn("reconcile: failed to prune skill dir",
				"skill", name, "error", rmErr)
		} else {
			w.logger.Info("reconcile: pruned stale skill directory", "skill", name, "reason", reason)
		}
	}
}

func isExtractedSkillDir(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "SKILL.md"))
	return err == nil
}

func (w *Worker) periodicReconcileLoop(ctx context.Context) {
	interval := w.cfg.ReconcileInterval
	if interval <= 0 {
		interval = defaultReconcileInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.signalReconcile(reconcileReasonPeriodic)
		}
	}
}

func (w *Worker) wireReconnectNotifier() {
	if rn, ok := w.svc.Broker().(broker.ReconnectNotifier); ok {
		rn.OnReconnect(func() {
			w.signalReconcile(reconcileReasonReconnect)
		})
	}
}

func strSet(names []string) map[string]bool {
	s := make(map[string]bool, len(names))
	for _, n := range names {
		s[n] = true
	}
	return s
}
