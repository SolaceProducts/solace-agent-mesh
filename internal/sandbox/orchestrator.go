// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sandbox

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

type Orchestrator struct {
	rt      *Runtime
	images  *ImageManager
	logger  *slog.Logger
	mu      sync.Mutex
	workers []*STRContainer
	stopped bool
}

func NewOrchestrator(rt *Runtime, logger *slog.Logger) *Orchestrator {
	return &Orchestrator{
		rt:     rt,
		images: NewImageManager(rt, logger),
		logger: logger.With("logger_name", "sandbox-orchestrator"),
	}
}

func (o *Orchestrator) Runtime() *Runtime {
	return o.rt
}

func (o *Orchestrator) ImageManager() *ImageManager {
	return o.images
}

func (o *Orchestrator) EnsureImage(ctx context.Context, sandboxDir string, progress func(msg string)) error {
	if o.images.ImageExists(ctx) {
		o.logger.Info("STR container image already exists", "tag", o.images.Tag())
		progress("STR container image ready")
		return nil
	}
	return o.images.BuildImage(ctx, sandboxDir, progress)
}

func (o *Orchestrator) LaunchSTR(ctx context.Context, cfg STRContainerConfig) (*STRContainer, error) {
	o.mu.Lock()
	if o.stopped {
		o.mu.Unlock()
		return nil, fmt.Errorf("orchestrator is stopped")
	}
	o.mu.Unlock()

	container := NewSTRContainer(o.rt, o.images.Tag(), o.logger)
	if err := container.Start(ctx, cfg); err != nil {
		return nil, fmt.Errorf("launch STR container %q: %w", cfg.Name, err)
	}

	o.mu.Lock()
	o.workers = append(o.workers, container)
	o.mu.Unlock()

	return container, nil
}

func (o *Orchestrator) StopAll(ctx context.Context) error {
	o.mu.Lock()
	o.stopped = true
	workers := make([]*STRContainer, len(o.workers))
	copy(workers, o.workers)
	o.mu.Unlock()

	var firstErr error
	for i := len(workers) - 1; i >= 0; i-- {
		if err := workers[i].Stop(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	o.mu.Lock()
	o.workers = nil
	o.mu.Unlock()

	return firstErr
}

func (o *Orchestrator) WorkerCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.workers)
}
