// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type BackgroundMonitorConfig struct {
	Enabled          bool
	CheckIntervalSec int
	DefaultTimeoutMs int64
}

func ParseBackgroundMonitorConfig(m map[string]any) BackgroundMonitorConfig {
	cfg := BackgroundMonitorConfig{
		Enabled:          true,
		CheckIntervalSec: 60,
		DefaultTimeoutMs: 3600000,
	}
	if m == nil {
		return cfg
	}
	if v, ok := m["enabled"].(bool); ok {
		cfg.Enabled = v
	}
	if v := getInt(m, "check_interval_sec"); v > 0 {
		cfg.CheckIntervalSec = v
	}
	if v := getInt(m, "default_timeout_ms"); v > 0 {
		cfg.DefaultTimeoutMs = int64(v)
	}
	return cfg
}

type BackgroundMonitorService struct {
	cfg      BackgroundMonitorConfig
	taskRepo *store.TaskRepo
	logger   *slog.Logger

	metrics runtime.Metrics
}

func (s *BackgroundMonitorService) metricsRecorder() runtime.Metrics {
	if s.metrics != nil {
		return s.metrics
	}
	return runtime.MetricProvider()
}

func NewBackgroundMonitorService(cfg BackgroundMonitorConfig, taskRepo *store.TaskRepo, logger *slog.Logger) *BackgroundMonitorService {
	return &BackgroundMonitorService{
		cfg:      cfg,
		taskRepo: taskRepo,
		logger:   logger.With("logger_name", "background-monitor"),
	}
}

func (s *BackgroundMonitorService) Start(ctx context.Context, wg *sync.WaitGroup) {
	s.logger.Info("background monitor service started",
		"check_interval_sec", s.cfg.CheckIntervalSec,
		"default_timeout_ms", s.cfg.DefaultTimeoutMs,
	)

	monitorLoop := func() {
		ticker := time.NewTicker(time.Duration(s.cfg.CheckIntervalSec) * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.checkStaleTasks(ctx)
			}
		}
	}

	if wg != nil {
		wg.Go(monitorLoop)
	} else {
		go monitorLoop()
	}
}

const MetricTaskTerminal = "gateway.task.terminal"

const staleTaskSweepLimit = 500

func (s *BackgroundMonitorService) checkStaleTasks(ctx context.Context) {
	tasks, err := s.taskRepo.FindStaleTasks(ctx, s.cfg.DefaultTimeoutMs, staleTaskSweepLimit)
	if err != nil {
		s.logger.Warn("failed to find stale tasks", "error", err)
		return
	}
	if len(tasks) == 0 {
		return
	}

	ids := make([]string, len(tasks))
	for i, task := range tasks {
		ids[i] = task.ID
	}

	affected, err := s.taskRepo.TimeoutTasks(ctx, ids)
	if err != nil {
		s.logger.Error("failed to timeout stale tasks", "candidateCount", len(ids), "error", err)
		return
	}
	if affected == 0 {
		return
	}

	s.metricsRecorder().Counter(MetricTaskTerminal, affected, "status", "timeout")
	s.logger.Warn("timed out stale tasks", "count", affected, "candidateCount", len(ids))
}
