// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

func (h *TestHarness) createSetupVolumes(ctx context.Context) error {
	ac := h.scenario.ConfigOverrides.AgentConfig
	if len(ac.SetupVolumes) == 0 {
		return nil
	}

	for _, sv := range ac.SetupVolumes {
		creatorType := volume.CallerHuman
		if sv.CreatorType == "agent" {
			creatorType = volume.CallerAgent
		}

		opts := volume.CreateOptions{
			ID:          sv.ID,
			Name:        sv.Name,
			UserID:      sv.UserID,
			CreatorType: creatorType,
		}

		if sv.TTL != "" {
			d, err := time.ParseDuration(sv.TTL)
			if err != nil {
				return fmt.Errorf("setup_volumes[%s]: invalid ttl %q: %w", sv.Name, sv.TTL, err)
			}
			opts.TTL = &d
		}

		id, err := h.volSvc.Create(ctx, opts)
		if err != nil {
			return fmt.Errorf("setup_volumes[%s]: create: %w", sv.Name, err)
		}

		if sv.SessionID != "" {
			if err := h.volSvc.AssociateSession(ctx, id, sv.SessionID); err != nil {
				return fmt.Errorf("setup_volumes[%s]: associate session: %w", sv.Name, err)
			}
		}

		h.logger.Info("pre-created setup volume", "id", id, "name", sv.Name)
	}
	return nil
}

func (h *TestHarness) convertVolumeSlots(slots []VolumeSlotConfig) []agent.VolumeSlot {
	result := make([]agent.VolumeSlot, len(slots))
	for i, s := range slots {
		result[i] = agent.VolumeSlot{
			Name:      s.Name,
			Provision: s.Provision,
			VolumeID:  s.VolumeID,
			TTL:       s.TTL,
		}
	}
	return result
}

func (h *TestHarness) AssertVolumeState(t *testing.T) {
	t.Helper()
	if len(h.scenario.ExpectedVolumeState) == 0 {
		return
	}

	ctx := context.Background()

	for i, assertion := range h.scenario.ExpectedVolumeState {
		prefix := fmt.Sprintf("expected_volume_state[%d]", i)

		if assertion.SessionID != "" && assertion.VolumeCount != nil {
			ids, err := h.volSvc.ListSessionVolumes(ctx, assertion.SessionID)
			if err != nil {
				t.Errorf("%s: list session volumes: %v", prefix, err)
				continue
			}
			if len(ids) != *assertion.VolumeCount {
				t.Errorf("%s: session %q volume count: got %d, want %d",
					prefix, assertion.SessionID, len(ids), *assertion.VolumeCount)
			}
			continue
		}

		userID := assertion.UserID
		if userID == "" {
			userID = h.effectiveUserID()
		}

		var creatorFilter *volume.CallerType
		if assertion.CreatorType != "" {
			ct := volume.CallerType(assertion.CreatorType)
			creatorFilter = &ct
		}

		volumes, err := h.volSvc.List(ctx, userID, volume.ListFilter{
			CreatorType: creatorFilter,
		})
		if err != nil {
			t.Errorf("%s: list volumes: %v", prefix, err)
			continue
		}

		if assertion.NameContains != "" {
			found := false
			for _, v := range volumes {
				if strings.Contains(v.Name, assertion.NameContains) {
					found = true
					break
				}
			}
			if !found {
				names := make([]string, len(volumes))
				for j, v := range volumes {
					names[j] = v.Name
				}
				t.Errorf("%s: no volume with name containing %q (found: %v)",
					prefix, assertion.NameContains, names)
			}
		}

		if assertion.VolumeCount != nil && assertion.SessionID == "" {
			if len(volumes) != *assertion.VolumeCount {
				t.Errorf("%s: volume count for user %q: got %d, want %d",
					prefix, userID, len(volumes), *assertion.VolumeCount)
			}
		}
	}
}
