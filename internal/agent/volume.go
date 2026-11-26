// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

type volumeResolveCtx struct {
	ctx             context.Context
	sp              *StatusPublisher
	userID          string
	sessionID       string
	callerVolumeIDs []string
	canPrompt       bool
}

func (a *Agent) ValidateVolumeConfig() error {
	if err := a.validateVolumeSlots(); err != nil {
		return err
	}
	return a.validateVolumeBindings()
}

func (a *Agent) validateVolumeSlots() error {
	if len(a.cfg.VolumeSlots) == 0 && len(a.cfg.ToolVolumeBindings) == 0 {
		return nil
	}

	slotsByName := make(map[string]*VolumeSlot, len(a.cfg.VolumeSlots))
	for i := range a.cfg.VolumeSlots {
		slotsByName[a.cfg.VolumeSlots[i].Name] = &a.cfg.VolumeSlots[i]
	}

	for i := range a.cfg.VolumeSlots {
		slot := &a.cfg.VolumeSlots[i]
		if slot.Provision == "auto_create" && slot.TTL != "" {
			if _, err := time.ParseDuration(slot.TTL); err != nil {
				return fmt.Errorf("volume slot %q has invalid TTL %q: %w", slot.Name, slot.TTL, err)
			}
		}
	}

	for toolName, bindings := range a.cfg.ToolVolumeBindings {
		for paramName, slotName := range bindings {
			if _, ok := slotsByName[slotName]; !ok {
				return fmt.Errorf("tool %q volume binding %q references unknown slot %q", toolName, paramName, slotName)
			}
		}
	}

	return nil
}

func (a *Agent) validateVolumeBindings() error {
	if len(a.cfg.ToolVolumeBindings) == 0 {
		return nil
	}

	return nil
}

func (a *Agent) resolveVolumeBindings(toolName string, vrc *volumeResolveCtx) ([]tool.VolumeBinding, error) {
	bindings, ok := a.cfg.ToolVolumeBindings[toolName]
	if !ok || len(bindings) == 0 {
		return nil, nil
	}

	slotsByName := make(map[string]*VolumeSlot, len(a.cfg.VolumeSlots))
	for i := range a.cfg.VolumeSlots {
		slotsByName[a.cfg.VolumeSlots[i].Name] = &a.cfg.VolumeSlots[i]
	}

	volumeParams := a.getToolVolumeParams(toolName)

	var resolved []tool.VolumeBinding
	for paramName, slotName := range bindings {
		slot, ok := slotsByName[slotName]
		if !ok {
			return nil, fmt.Errorf("volume binding %q -> slot %q: slot not found", paramName, slotName)
		}

		var volumeID string

		switch slot.Provision {
		case "static":
			volumeID = slot.VolumeID

		case "auto_create":
			id, err := a.resolveAutoCreate(slot, vrc)
			if err != nil {
				return nil, fmt.Errorf("volume slot %q auto_create: %w", slotName, err)
			}
			volumeID = id

		case "prompt_user":
			id, err := a.resolvePromptUser(slot, vrc)
			if err != nil {
				return nil, fmt.Errorf("volume slot %q prompt_user: %w", slotName, err)
			}
			volumeID = id

		default:
			return nil, fmt.Errorf("volume slot %q has unknown provision strategy %q", slotName, slot.Provision)
		}

		vb := tool.VolumeBinding{
			VolumeID:  volumeID,
			AllowExec: slot.AllowExec,
		}
		if vp, ok := volumeParams[paramName]; ok {
			vb.MountPath = vp.MountPath
			vb.ReadOnly = vp.Mode == "readonly"
		}
		resolved = append(resolved, vb)
	}
	return resolved, nil
}

func volumeCacheKey(sessionID, slotName string) string {
	if sessionID == "" {
		return "\x00" + slotName
	}
	return sessionID + "\x00" + slotName
}

func volumeCacheKeyPrefix(sessionID string) string {
	if sessionID == "" {
		return "\x00"
	}
	return sessionID + "\x00"
}

func (a *Agent) resolvedVolumeIDsForSession(sessionID string) []string {
	prefix := volumeCacheKeyPrefix(sessionID)
	a.resolvedVolumesMu.Lock()
	defer a.resolvedVolumesMu.Unlock()
	if len(a.resolvedVolumes) == 0 {
		return nil
	}
	out := make([]string, 0, len(a.resolvedVolumes))
	for key, vid := range a.resolvedVolumes {
		if strings.HasPrefix(key, prefix) {
			out = append(out, vid)
		}
	}
	return out
}

func (a *Agent) resolveAutoCreate(slot *VolumeSlot, vrc *volumeResolveCtx) (string, error) {
	if vrc == nil {
		return "", fmt.Errorf("auto_create requires dispatch context")
	}
	cacheKey := volumeCacheKey(vrc.sessionID, slot.Name)

	a.resolvedVolumesMu.Lock()
	if id, ok := a.resolvedVolumes[cacheKey]; ok {
		a.resolvedVolumesMu.Unlock()
		return id, nil
	}
	a.resolvedVolumesMu.Unlock()

	if a.svc == nil || a.svc.Volumes() == nil {
		return "", fmt.Errorf("volume service not available")
	}
	volSvc := a.svc.Volumes()

	if vrc.sessionID != "" {
		id, ok, err := a.findExistingSessionVolumeForSlot(vrc, slot)
		if err != nil {
			return "", fmt.Errorf("find existing session volume: %w", err)
		}
		if ok {
			a.resolvedVolumesMu.Lock()
			a.resolvedVolumes[cacheKey] = id
			a.resolvedVolumesMu.Unlock()
			return id, nil
		}
	}

	ttl, err := time.ParseDuration(slot.TTL)
	if err != nil {
		return "", fmt.Errorf("invalid TTL %q: %w", slot.TTL, err)
	}

	volName := fmt.Sprintf("%s-%s", a.cfg.Name, slot.Name)

	id, err := createOrAdoptSlotVolume(vrc.ctx, volSvc, volume.CreateOptions{
		ID:          sessionSlotVolumeID(vrc.sessionID, vrc.userID, a.cfg.Name, slot.Name),
		Name:        volName,
		Description: fmt.Sprintf("Auto-created for agent %q slot %q", a.cfg.Name, slot.Name),
		UserID:      vrc.userID,
		CreatorType: volume.CallerAgent,
		CreatorName: a.cfg.Name,
		TTL:         &ttl,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create volume: %w", err)
	}

	a.resolvedVolumesMu.Lock()
	if existing, ok := a.resolvedVolumes[cacheKey]; ok {
		a.resolvedVolumesMu.Unlock()
		if existing != id {
			if delErr := volSvc.Delete(vrc.ctx, id, volume.CallerAgent); delErr != nil {
				a.logger.Warn("failed to delete duplicate auto-created volume", "volumeID", id, "error", delErr)
			}
		}
		return existing, nil
	}
	a.resolvedVolumes[cacheKey] = id
	a.resolvedVolumesMu.Unlock()

	if vrc.sessionID != "" {
		if assocErr := volSvc.AssociateSession(vrc.ctx, id, vrc.sessionID); assocErr != nil {
			a.logger.Warn("associate auto-created volume with session", "volumeID", id, "sessionID", vrc.sessionID, "error", assocErr)
		}
	}

	return id, nil
}

func sessionSlotVolumeID(sessionID, userID, agentName, slotName string) string {
	if sessionID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{sessionID, userID, agentName, slotName}, "\x00")))
	return "vol-" + hex.EncodeToString(sum[:16])
}

const slotVolumeCreateAttempts = 3

func createOrAdoptSlotVolume(ctx context.Context, volSvc volume.Service, opts volume.CreateOptions) (string, error) {
	for range slotVolumeCreateAttempts {
		id, err := volSvc.Create(ctx, opts)
		if !errors.Is(err, volume.ErrAlreadyExists) {
			return id, err
		}
		existing, err := volSvc.Get(ctx, opts.ID)
		if errors.Is(err, volume.ErrNotFound) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("get the volume already created for this session slot: %w", err)
		}
		if existing.UserID != opts.UserID {
			return "", fmt.Errorf("volume %s of this session slot belongs to another user", opts.ID)
		}
		if !existing.IsExpired() {
			return existing.ID, nil
		}
		if _, err := volSvc.RemoveIfExpired(ctx, opts.ID); err != nil {
			return "", fmt.Errorf("replace the expired volume of this session slot: %w", err)
		}
	}
	return "", fmt.Errorf("volume %s of this session slot kept being replaced by other tasks", opts.ID)
}

func (a *Agent) findExistingSessionVolumeForSlot(vrc *volumeResolveCtx, slot *VolumeSlot) (string, bool, error) {
	volSvc := a.svc.Volumes()
	wantName := fmt.Sprintf("%s-%s", a.cfg.Name, slot.Name)
	sessionID := vrc.sessionID
	ids, err := volSvc.ListSessionVolumes(vrc.ctx, sessionID)
	if err != nil {
		return "", false, fmt.Errorf("list session volumes: %w", err)
	}
	if len(ids) == 0 {
		return "", false, nil
	}
	for _, id := range ids {
		v, getErr := volSvc.Get(vrc.ctx, id)
		if getErr != nil {
			return "", false, fmt.Errorf("get session volume %s: %w", id, getErr)
		}
		if v == nil {
			continue
		}
		if v.IsExpired() {
			continue
		}
		if v.CreatorName != a.cfg.Name {
			continue
		}
		if vrc.userID != "" && v.UserID != vrc.userID {
			continue
		}
		if v.Name != wantName {
			continue
		}
		return id, true, nil
	}
	return "", false, nil
}

func volumePromptRequestID(slotName string) string {
	return "volume-prompt-" + slotName + "-" + idgen.New()
}

func (a *Agent) resolvePromptUser(slot *VolumeSlot, vrc *volumeResolveCtx) (string, error) {
	if vrc == nil {
		return "", fmt.Errorf("prompt_user requires dispatch context")
	}
	cacheKey := volumeCacheKey(vrc.sessionID, slot.Name)

	a.resolvedVolumesMu.Lock()
	if id, ok := a.resolvedVolumes[cacheKey]; ok {
		a.resolvedVolumesMu.Unlock()
		return id, nil
	}
	a.resolvedVolumesMu.Unlock()

	if !vrc.canPrompt {
		return "", fmt.Errorf("this channel can't show an interactive volume picker; "+
			"bind volume slot %q to a static volume or use the web UI", slot.Name)
	}

	if a.svc == nil || a.svc.Volumes() == nil {
		return "", fmt.Errorf("volume service not available")
	}
	volSvc := a.svc.Volumes()

	volumes, err := volSvc.List(vrc.ctx, vrc.userID, volume.ListFilter{})
	if err != nil {
		return "", fmt.Errorf("failed to list volumes: %w", err)
	}

	volumes = a.prioritizeVolumes(vrc, volSvc, volumes)

	requestID := volumePromptRequestID(slot.Name)
	timeout := 10 * time.Minute

	promptMessage := slot.PromptMessage
	if promptMessage == "" {
		promptMessage = fmt.Sprintf("Select a volume for %q", slot.Name)
	}

	surface, err := generateVolumePromptSurface(requestID, promptMessage, volumes, timeout)
	if err != nil {
		return "", fmt.Errorf("failed to generate volume prompt surface: %w", err)
	}

	resp := a.waitForUserInput(vrc.ctx, vrc.sp, hilRequest{
		requestID:       requestID,
		source:          "volume_prompt",
		recipientUserID: vrc.userID,
		sessionID:       vrc.sessionID,
		surface:         surface,
		webInboxOnly:    false,
	}, timeout)

	switch resp.Status {
	case "answered":
		selectedVolume, _ := resp.Data["selected_volume"].(string)
		manualID, _ := resp.Data["manual_volume_id"].(string)

		var volumeID string
		if selectedVolume == "__other__" || selectedVolume == "" {
			volumeID = manualID
		} else {
			volumeID = selectedVolume
		}

		if volumeID == "" {
			return "", fmt.Errorf("no volume selected")
		}

		v, err := volSvc.Get(vrc.ctx, volumeID)
		if err != nil {
			return "", fmt.Errorf("selected volume %q: %w", volumeID, err)
		}
		if vrc.userID != "" && v.UserID != vrc.userID {
			return "", fmt.Errorf("selected volume %q is not available", volumeID)
		}

		a.resolvedVolumesMu.Lock()
		if existing, ok := a.resolvedVolumes[cacheKey]; ok {
			a.resolvedVolumesMu.Unlock()
			return existing, nil
		}
		a.resolvedVolumes[cacheKey] = volumeID
		a.resolvedVolumesMu.Unlock()

		if vrc.sessionID != "" {
			if assocErr := volSvc.AssociateSession(vrc.ctx, volumeID, vrc.sessionID); assocErr != nil {
				a.logger.Warn("associate user-picked volume with session", "volumeID", volumeID, "sessionID", vrc.sessionID, "error", assocErr)
			}
		}

		return volumeID, nil

	case "cancelled":
		return "", fmt.Errorf("volume selection cancelled by user")

	case "timed_out":
		return "", fmt.Errorf("volume selection timed out")

	default:
		return "", fmt.Errorf("unexpected volume prompt response status: %q", resp.Status)
	}
}

func (a *Agent) getToolVolumeParams(toolName string) map[string]tool.VolumeParamInfo {
	t := a.tools.GetTool(toolName)
	if t == nil {
		return nil
	}
	vpp, ok := t.(tool.VolumeParamProvider)
	if !ok {
		return nil
	}
	params := vpp.VolumeParams()
	if len(params) == 0 {
		return nil
	}
	result := make(map[string]tool.VolumeParamInfo, len(params))
	for _, p := range params {
		result[p.Name] = p
	}
	return result
}

func (a *Agent) prioritizeVolumes(vrc *volumeResolveCtx, volSvc volume.Service, volumes []*volume.Volume) []*volume.Volume {
	if len(volumes) == 0 {
		return volumes
	}

	prioritySet := make(map[string]bool)
	for _, id := range vrc.callerVolumeIDs {
		prioritySet[id] = true
	}
	if vrc.sessionID != "" {
		if sessionVols, err := volSvc.ListSessionVolumes(vrc.ctx, vrc.sessionID); err == nil {
			for _, id := range sessionVols {
				prioritySet[id] = true
			}
		}
	}
	if len(prioritySet) == 0 {
		return volumes
	}

	prioritized := make([]*volume.Volume, 0, len(volumes))
	rest := make([]*volume.Volume, 0, len(volumes))
	for _, v := range volumes {
		if prioritySet[v.ID] {
			prioritized = append(prioritized, v)
		} else {
			rest = append(rest, v)
		}
	}
	return append(prioritized, rest...)
}
