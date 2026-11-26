// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"errors"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func (g *GatewayCore) startWebuiSettingsWatch(loopCtx context.Context, brk broker.Broker) error {
	if g.webuiSettingsRegistry == nil {
		return nil
	}
	sub, err := brk.CreateDirectSubscription()
	if err != nil {
		return err
	}
	topic := a2a.WebuiSettingsUpdateTopic(g.cfg.Namespace)
	if err := brk.Subscribe(sub, topic); err != nil {
		if delErr := brk.DeleteQueue(sub.Name()); delErr != nil {
			g.logger.Warn("webui settings receiver cleanup after failed subscribe", "error", delErr)
		}
		return err
	}
	g.webuiSettingsSub = sub

	g.webuiSettingsRegistry.Warm()

	g.loopWg.Add(2)
	go func() {
		defer g.loopWg.Done()
		g.webuiSettingsRegistry.RunRefreshLoop(loopCtx)
	}()
	go func() {
		defer g.loopWg.Done()
		g.webuiSettingsWatchLoop(loopCtx)
	}()
	return nil
}

func (g *GatewayCore) webuiSettingsWatchLoop(ctx context.Context) {
	bo := backoff.Exponential{
		Initial: 100 * time.Millisecond,
		Max:     30 * time.Second,
		Jitter:  0.2,
	}
	for {
		msg, err := g.svc.Broker().Receive(ctx, g.webuiSettingsSub)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, broker.ErrQueueNotFound) {
				g.logger.Warn("webui settings watch exiting: subscription no longer exists")
				return
			}
			g.logger.Warn("webui settings update receive error", "error", err, "backoff", bo.Current())
			if waitErr := bo.Wait(ctx); waitErr != nil {
				return
			}
			continue
		}
		bo.Reset()
		if msg == nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		g.webuiSettingsRegistry.Refresh()
		msg.Ack()
	}
}

func (g *GatewayCore) stopWebuiSettingsWatch(brk broker.Broker) {
	sub := g.webuiSettingsSub
	if sub == nil {
		return
	}
	topic := a2a.WebuiSettingsUpdateTopic(g.cfg.Namespace)
	if err := brk.Unsubscribe(sub, topic); err != nil {
		g.logger.Warn("webui settings unsubscribe failed", "error", err)
	}
	if err := brk.DeleteQueue(sub.Name()); err != nil {
		g.logger.Warn("webui settings receiver cleanup failed", "error", err)
	}
}
