// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"errors"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/discovery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type peerDirectories struct {
	svc         runtime.Services
	mu          sync.Mutex
	byNamespace map[string]*discovery.Directory
}

func newPeerDirectories(svc runtime.Services) *peerDirectories {
	return &peerDirectories{svc: svc, byNamespace: make(map[string]*discovery.Directory)}
}

func (d *peerDirectories) forNamespace(namespace string) *discovery.Directory {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	dir, ok := d.byNamespace[namespace]
	if !ok {
		dir = discovery.NewDirectory(d.svc.Broker(), namespace, d.svc.Logger())
		d.byNamespace[namespace] = dir
	}
	return dir
}

func (d *peerDirectories) close(ctx context.Context) error {
	d.mu.Lock()
	dirs := make([]*discovery.Directory, 0, len(d.byNamespace))
	for _, dir := range d.byNamespace {
		dirs = append(dirs, dir)
	}
	d.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		errs := make([]error, 0, len(dirs))
		for _, dir := range dirs {
			errs = append(errs, dir.Close())
		}
		done <- errors.Join(errs...)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
