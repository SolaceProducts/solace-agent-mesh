// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"slices"
	"sync"
)

type devOwner struct{ _ byte }

type devBind struct {
	owner       *devOwner
	onBindState func(BindState)
	promoted    chan struct{}
	released    chan struct{}

	active bool
	quiet  bool

	reportMu sync.Mutex
	reported BindState
}

func newDevBind(owner *devOwner, onBindState func(BindState)) *devBind {
	return &devBind{
		owner:       owner,
		onBindState: onBindState,
		promoted:    make(chan struct{}),
		released:    make(chan struct{}),
		reported:    BindStandby,
	}
}

type devExclusiveQueue struct {
	binds []*devBind
}

func (q *devExclusiveQueue) indexOf(owner *devOwner) int {
	return slices.IndexFunc(q.binds, func(b *devBind) bool { return b.owner == owner })
}

func (q *devExclusiveQueue) bindOf(owner *devOwner) *devBind {
	if i := q.indexOf(owner); i >= 0 {
		return q.binds[i]
	}
	return nil
}

func (b *DevBroker) bindLocked(owner *devOwner, name string, opts QueueOptions) (bind *devBind, ok bool) {
	if !opts.Named {
		return nil, false
	}
	xq, exists := b.exclusive[name]
	if !exists {
		if !opts.Exclusive {
			return nil, false
		}
		xq = &devExclusiveQueue{}
		b.exclusive[name] = xq
	}
	if xq.indexOf(owner) >= 0 {
		return nil, true
	}
	added := newDevBind(owner, opts.OnBindState)
	if len(xq.binds) == 0 {
		added.active = true
		close(added.promoted)
		bind = added
	}
	xq.binds = append(xq.binds, added)
	return bind, true
}

func (b *DevBroker) releaseLocked(owner *devOwner, name string, lost bool) []*devBind {
	xq := b.exclusive[name]
	if xq == nil {
		return nil
	}
	i := xq.indexOf(owner)
	if i < 0 {
		return nil
	}
	ended := xq.binds[i]
	xq.binds = slices.Delete(xq.binds, i, i+1)
	wasActive := ended.active
	ended.active = false
	ended.quiet = !lost
	close(ended.released)
	changed := []*devBind{ended}

	if len(xq.binds) == 0 {
		delete(b.exclusive, name)
		b.closeQueueLocked(name)
		return changed
	}
	if wasActive {
		next := xq.binds[0]
		next.active = true
		close(next.promoted)
		changed = append(changed, next)
	}
	return changed
}

func (b *DevBroker) dropExclusiveLocked(name string) {
	xq := b.exclusive[name]
	if xq == nil {
		return
	}
	for _, bind := range xq.binds {
		bind.active = false
		bind.quiet = true
		close(bind.released)
	}
	delete(b.exclusive, name)
}

func (b *DevBroker) releaseBind(owner *devOwner, name string) {
	b.mu.Lock()
	changed := b.releaseLocked(owner, name, false)
	b.mu.Unlock()
	b.notify(changed...)
}

func (b *DevBroker) releaseAll(owner *devOwner) {
	b.mu.Lock()
	var changed []*devBind
	for name, xq := range b.exclusive {
		if xq.indexOf(owner) >= 0 {
			changed = append(changed, b.releaseLocked(owner, name, true)...)
		}
	}
	b.mu.Unlock()
	b.notify(changed...)
}

func (b *DevBroker) notify(binds ...*devBind) {
	for _, bind := range binds {
		bind.reportMu.Lock()
		b.mu.RLock()
		state, quiet := BindStandby, bind.quiet
		if bind.active {
			state = BindActive
		}
		b.mu.RUnlock()
		if !quiet && state != bind.reported && bind.onBindState != nil {
			bind.reported = state
			bind.onBindState(state)
		}
		bind.reportMu.Unlock()
	}
}
