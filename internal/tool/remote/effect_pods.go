// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
)

const podBroadcastTTL = 3 * time.Minute

type podEffect struct {
	static      string
	aware       string
	hasAware    bool
	fingerprint string
	seen        time.Time
}

type podEffects map[string]*podEffect

func (p podEffects) expire(now time.Time) {
	for id, pod := range p {
		if now.Sub(pod.seen) >= podBroadcastTTL {
			delete(p, id)
		}
	}
}

func (p podEffects) recordBroadcast(id, fingerprint, effect string, now time.Time) {
	pod, known := p[id]
	if !known {
		pod = &podEffect{}
		p[id] = pod
	}
	changed := pod.fingerprint != "" && pod.fingerprint != fingerprint
	firstAfterReply := pod.fingerprint == "" && pod.hasAware && effect != remotetool.EffectReadOnly
	if changed || firstAfterReply {
		pod.hasAware = false
		pod.aware = ""
	}
	pod.static = effect
	pod.fingerprint = fingerprint
	pod.seen = now
}

func (p podEffects) recordReply(id, effect string, now time.Time) {
	pod, known := p[id]
	if !known {
		pod = &podEffect{static: effect, seen: now}
		p[id] = pod
	}
	pod.aware = effect
	pod.hasAware = true
}

func (p podEffects) merged() (string, bool) {
	if len(p) == 0 {
		return "", false
	}
	merged := remotetool.EffectReadOnly
	for _, pod := range p {
		effect := pod.static
		if pod.hasAware {
			effect = pod.aware
		}
		switch effect {
		case remotetool.EffectReadOnly:
		case remotetool.EffectSideEffecting:
			return remotetool.EffectSideEffecting, true
		default:
			merged = ""
		}
	}
	return merged, true
}
