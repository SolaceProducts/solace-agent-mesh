// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const (
	inboundLease          = 2 * time.Minute
	inboundMaxAttempts    = 3
	inboundGiveUpAttempts = 2 * inboundMaxAttempts
	inboundRecoveryBatch  = 50
	inboundRetention      = 8 * 24 * time.Hour
	inboundReplayWindow   = time.Hour
	inboundClaimTimeout   = 2 * time.Second
)

type InboundOutcome int

const (
	InboundProcess InboundOutcome = iota
	InboundSkip
)

type InboundClaim struct {
	Outcome InboundOutcome
	Token   int64
}

type InboundMessage struct {
	ID       string
	Payload  string
	Token    int64
	Attempts int
}

type InboundReplayer interface {
	ReplayInbound(ctx context.Context, msg InboundMessage)
	AbandonInbound(ctx context.Context, msg InboundMessage) bool
}

func (g *GatewayCore) AcceptInbound(ctx context.Context, messageID, payload string) (InboundClaim, error) {
	if g.inbound == nil || messageID == "" {
		return InboundClaim{Outcome: InboundProcess}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, inboundClaimTimeout)
	defer cancel()
	claim, err := g.inbound.Claim(ctx, g.cfg.GatewayID, messageID, payload, inboundLease)
	if err != nil {
		return InboundClaim{}, err
	}
	if claim.Outcome != store.ClaimAcquired {
		g.logger.Debug("inbound message already taken", "messageID", messageID, "outcome", claim.Outcome)
		return InboundClaim{Outcome: InboundSkip}, nil
	}
	return InboundClaim{Outcome: InboundProcess, Token: claim.Token}, nil
}

func (g *GatewayCore) InboundRenew(messageID string, token int64) (int64, bool) {
	if g.inbound == nil || token == 0 {
		return token, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), dbTaskRegistryTimeout)
	defer cancel()
	renewed, held, err := g.inbound.Renew(ctx, g.cfg.GatewayID, messageID, token)
	if err != nil {
		g.logger.Warn("inbound message lease not renewed; submitting it", "messageID", messageID, "error", err)
		return token, true
	}
	if !held {
		return token, false
	}
	return renewed, true
}

func (g *GatewayCore) InboundDone(messageID string, token int64) {
	if g.inbound == nil || token == 0 {
		return
	}
	var held bool
	err := g.inboundWrite(func(ctx context.Context) (err error) {
		held, err = g.inbound.MarkDone(ctx, g.cfg.GatewayID, messageID, token)
		return err
	})
	if err != nil {
		g.logger.Warn("inbound message done write failed; it will be processed again after its lease", "messageID", messageID, "error", err)
		return
	}
	if !held {
		g.logger.Warn("inbound message was taken over before it was done", "messageID", messageID)
	}
}

func (g *GatewayCore) inboundWrite(write func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbTaskRegistryTimeout)
	err := write(ctx)
	cancel()
	if err == nil {
		return nil
	}
	time.Sleep(200 * time.Millisecond)
	ctx, cancel = context.WithTimeout(context.Background(), dbTaskRegistryTimeout)
	defer cancel()
	return write(ctx)
}

func (g *GatewayCore) recoverInbound(ctx context.Context) {
	replayer, ok := g.adapter.(InboundReplayer)
	if !ok || g.inbound == nil {
		return
	}
	queryCtx, cancel := context.WithTimeout(ctx, dbTaskRegistryTimeout)
	lapsed, err := g.inbound.TakeLapsed(queryCtx, g.cfg.GatewayID, inboundLease, inboundRecoveryBatch)
	cancel()
	if err != nil {
		g.logger.Warn("taking over lapsed inbound messages failed", "error", err)
	}
	for _, l := range lapsed {
		msg := InboundMessage{ID: l.MessageID, Payload: l.Payload, Token: l.Token, Attempts: l.Attempts}
		if msg.Attempts > inboundGiveUpAttempts {
			g.logger.Error("inbound message dropped; the user could not be told it was not processed",
				"messageID", msg.ID, "attempts", msg.Attempts, "receivedAt", l.ReceivedAt)
			g.InboundDone(msg.ID, msg.Token)
			continue
		}
		g.loopWg.Add(1)
		if msg.Attempts > inboundMaxAttempts || time.Since(l.ReceivedAt) > inboundReplayWindow {
			g.logger.Warn("inbound message abandoned; telling the user",
				"messageID", msg.ID, "attempts", msg.Attempts, "receivedAt", l.ReceivedAt)
			go func() {
				defer g.loopWg.Done()
				if replayer.AbandonInbound(ctx, msg) {
					g.InboundDone(msg.ID, msg.Token)
				}
			}()
			continue
		}
		g.logger.Info("finishing an inbound message another instance held", "messageID", msg.ID, "attempts", msg.Attempts)
		go func() {
			defer g.loopWg.Done()
			replayer.ReplayInbound(ctx, msg)
		}()
	}
}

func (g *GatewayCore) pruneInbound(ctx context.Context) error {
	if g.inbound == nil {
		return nil
	}
	pruneCtx, cancel := context.WithTimeout(ctx, dbTaskRegistryTimeout)
	defer cancel()
	n, err := g.inbound.DeleteOlderThan(pruneCtx, time.Now().Add(-inboundRetention), 0)
	if err != nil {
		return fmt.Errorf("prune inbound message records: %w", err)
	}
	if n > 0 {
		g.logger.Debug("pruned inbound message records", "count", n)
	}
	return nil
}
