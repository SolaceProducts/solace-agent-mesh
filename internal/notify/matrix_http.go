// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type NotificationMatrixDTO struct {
	Categories []MatrixCategoryDTO `json:"categories"`
	Channels   []MatrixChannelDTO  `json:"channels"`
	Cells      []MatrixCellDTO     `json:"cells"`
}

type MatrixCategoryDTO struct {
	Category    string `json:"category"`
	Label       string `json:"label"`
	Group       string `json:"group"`
	Description string `json:"description"`
}

type MatrixChannelDTO struct {
	ChannelType    string `json:"channelType"`
	DestinationKey string `json:"destinationKey"`
	Destination    string `json:"destination"`
}

type MatrixCellDTO struct {
	Category       string `json:"category"`
	ChannelType    string `json:"channelType"`
	DestinationKey string `json:"destinationKey"`
	Enabled        bool   `json:"enabled"`
	Locked         bool   `json:"locked"`
}

type UpdateNotificationMatrixRequest struct {
	Cells []UpdateMatrixCellRequest `json:"cells"`
}

type UpdateMatrixCellRequest struct {
	Category       string `json:"category"`
	ChannelType    string `json:"channelType"`
	DestinationKey string `json:"destinationKey"`
	Enabled        *bool  `json:"enabled"`
}

const (
	msgMatrixDisabled   = "preferences are not enabled (no database configured)"
	msgMatrixLoadFailed = "failed to load notification settings"
)

var inboxColumn = MatrixChannelDTO{ChannelType: inboxChannelType}

func lockedCell(c Category, ch MatrixChannelDTO) bool {
	return ch.ChannelType == inboxChannelType && !c.GatesInbox
}

type matrixState struct {
	categories          []Category
	channels            []MatrixChannelDTO
	unconfirmedChannels []MatrixChannelDTO
	mutedCategories     map[string]bool
	disabledChannels    map[string]bool
	disabledCells       map[string]bool
	reachableByPrefKey  map[string]bool
}

func (n *NotifyCore) loadMatrixState(ctx context.Context, caller *auth.Subject, userID string, destinationKeys []string) (matrixState, error) {
	disabledChannels, err := n.prefs.DisabledChannels(ctx, userID)
	if err != nil {
		return matrixState{}, fmt.Errorf("channel preferences: %w", err)
	}
	mutedCategories, err := n.categoryPrefs.DisabledCategories(ctx, userID)
	if err != nil {
		return matrixState{}, fmt.Errorf("category preferences: %w", err)
	}
	disabledCells, err := n.cellPrefs.DisabledCells(ctx, userID)
	if err != nil {
		return matrixState{}, fmt.Errorf("cell preferences: %w", err)
	}
	reachable, unconfirmed, err := n.preferenceChannels(ctx, destinationKeys, nil)
	if err != nil {
		return matrixState{}, fmt.Errorf("channels: %w", err)
	}
	state := matrixState{
		categories:         n.offeredCategories(caller),
		channels:           make([]MatrixChannelDTO, 0, len(reachable)+1),
		mutedCategories:    mutedCategories,
		disabledChannels:   disabledChannels,
		disabledCells:      disabledCells,
		reachableByPrefKey: make(map[string]bool, len(reachable)+1),
	}
	state.channels = append(state.channels, inboxColumn)
	state.reachableByPrefKey[store.PrefKey(inboxChannelType, "")] = true
	for _, p := range unconfirmed {
		state.unconfirmedChannels = append(state.unconfirmedChannels, MatrixChannelDTO{ChannelType: p.ChannelType, DestinationKey: p.DestinationKey, Destination: p.Destination})
	}
	for _, p := range reachable {
		state.channels = append(state.channels, MatrixChannelDTO{ChannelType: p.ChannelType, DestinationKey: p.DestinationKey, Destination: p.Destination})
		state.reachableByPrefKey[store.PrefKey(p.ChannelType, p.DestinationKey)] = true
	}
	return state, nil
}

func (s matrixState) enabled(c Category, ch MatrixChannelDTO) bool {
	if lockedCell(c, ch) {
		return true
	}
	if s.disabledCells[store.CellKey(c.Key, ch.ChannelType, ch.DestinationKey)] {
		return false
	}
	if ch.ChannelType == inboxChannelType {
		return !s.mutedCategories[c.Key]
	}
	return !s.mutedCategories[c.Key] && !s.disabledChannels[store.PrefKey(ch.ChannelType, ch.DestinationKey)]
}

func (s matrixState) withOptOuts(o store.OptOuts) matrixState {
	s.mutedCategories = o.MutedCategories
	s.disabledChannels = o.DisabledChannels
	s.disabledCells = o.DisabledCells
	return s
}

func (s matrixState) dto() NotificationMatrixDTO {
	cats := s.categories
	out := NotificationMatrixDTO{
		Categories: make([]MatrixCategoryDTO, 0, len(cats)),
		Channels:   s.channels,
		Cells:      make([]MatrixCellDTO, 0, len(cats)*len(s.channels)),
	}
	for _, c := range cats {
		out.Categories = append(out.Categories, MatrixCategoryDTO{Category: c.Key, Label: c.Label, Group: c.Group, Description: c.Description})
		for _, ch := range s.channels {
			out.Cells = append(out.Cells, MatrixCellDTO{
				Category: c.Key, ChannelType: ch.ChannelType, DestinationKey: ch.DestinationKey,
				Enabled: s.enabled(c, ch), Locked: lockedCell(c, ch),
			})
		}
	}
	return out
}

func (s matrixState) lookupOffered(key string) (Category, error) {
	for _, c := range s.categories {
		if c.Key == key {
			return c, nil
		}
	}
	return Category{}, unknownCategoryError(key)
}

func (n *NotifyCore) matrixEnabled(w http.ResponseWriter) bool {
	if n.prefs == nil || n.categoryPrefs == nil || n.cellPrefs == nil {
		writeError(w, http.StatusServiceUnavailable, msgMatrixDisabled, n.logger)
		return false
	}
	return true
}

func (n *NotifyCore) handleMatrixGet(w http.ResponseWriter, r *http.Request) {
	if !n.matrixEnabled(w) {
		return
	}
	userID, ok := n.targetUser(w, r, rbac.ScopeNotifyReadOthers)
	if !ok {
		return
	}
	state, err := n.loadMatrixState(r.Context(), requestCaller(r), userID, requestDestinationKeys(r, userID))
	if err != nil {
		writeServerError(w, msgMatrixLoadFailed, err, n.logger)
		return
	}
	writeJSON(w, http.StatusOK, state.dto())
}

func (n *NotifyCore) handleMatrixUpdate(w http.ResponseWriter, r *http.Request) {
	if !n.matrixEnabled(w) {
		return
	}
	userID, ok := n.targetUser(w, r, rbac.ScopeNotifySendOthers)
	if !ok {
		return
	}
	var req UpdateNotificationMatrixRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error(), n.logger)
		return
	}
	state, err := n.loadMatrixState(r.Context(), requestCaller(r), userID, requestDestinationKeys(r, userID))
	if err != nil {
		writeServerError(w, msgMatrixLoadFailed, err, n.logger)
		return
	}
	cells, problems := validateMatrixCells(req.Cells, state)
	if len(problems) > 0 {
		writeError(w, http.StatusBadRequest, "invalid notification settings: "+strings.Join(problems, "; "), n.logger)
		return
	}
	preserve := func(optOuts store.OptOuts) []store.Cell { return state.withOptOuts(optOuts).preserveOthers(cells) }
	if saveErr := n.cellPrefs.ReplaceMatrix(r.Context(), userID, cells, preserve); saveErr != nil {
		writeServerError(w, "failed to save notification settings", saveErr, n.logger)
		return
	}
	updated, err := n.loadMatrixState(r.Context(), requestCaller(r), userID, requestDestinationKeys(r, userID))
	if err != nil {
		writeServerError(w, msgMatrixLoadFailed, err, n.logger)
		return
	}
	writeJSON(w, http.StatusOK, updated.dto())
}

func validateMatrixCells(reqCells []UpdateMatrixCellRequest, state matrixState) ([]store.Cell, []string) {
	cells := make([]store.Cell, 0, len(reqCells))
	seen := make(map[string]bool, len(reqCells))
	var problems []string
	for i, rc := range reqCells {
		cat, err := state.lookupOffered(rc.Category)
		cellKey := store.CellKey(rc.Category, rc.ChannelType, rc.DestinationKey)
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("cells[%d]: %v", i, err))
		case seen[cellKey]:
			problems = append(problems, fmt.Sprintf("cells[%d]: %q on %q is already set by an earlier cell", i, rc.Category, rc.ChannelType))
		case !state.reachableByPrefKey[store.PrefKey(rc.ChannelType, rc.DestinationKey)]:
			problems = append(problems, fmt.Sprintf("cells[%d]: you have no %q channel with destination key %q", i, rc.ChannelType, rc.DestinationKey))
		case rc.Enabled == nil:
			problems = append(problems, fmt.Sprintf("cells[%d]: enabled is required", i))
		case !*rc.Enabled && lockedCell(cat, MatrixChannelDTO{ChannelType: rc.ChannelType}):
			problems = append(problems, fmt.Sprintf("cells[%d]: %s always reach the in-app inbox", i, cat.Label))
		default:
			seen[cellKey] = true
			cells = append(cells, store.Cell{Category: cat.Key, ChannelType: rc.ChannelType, Destination: rc.DestinationKey, Enabled: *rc.Enabled})
		}
	}
	return cells, problems
}

func (s matrixState) preserveOthers(requested []store.Cell) []store.Cell {
	inRequest := make(map[string]bool, len(requested))
	touchedCategories := map[string]bool{}
	touchedChannels := map[string]bool{}
	for _, c := range requested {
		inRequest[store.CellKey(c.Category, c.ChannelType, c.Destination)] = true
		touchedCategories[c.Category] = true
		touchedChannels[store.PrefKey(c.ChannelType, c.Destination)] = true
	}
	var out []store.Cell
	for _, cat := range Categories() {
		for _, ch := range slices.Concat(s.channels, s.unconfirmedChannels) {
			key := store.CellKey(cat.Key, ch.ChannelType, ch.DestinationKey)
			if inRequest[key] || lockedCell(cat, ch) || s.enabled(cat, ch) {
				continue
			}
			rowCleared := touchedCategories[cat.Key] && s.mutedCategories[cat.Key]
			columnCleared := touchedChannels[store.PrefKey(ch.ChannelType, ch.DestinationKey)] &&
				s.disabledChannels[store.PrefKey(ch.ChannelType, ch.DestinationKey)]
			if rowCleared || columnCleared {
				out = append(out, store.Cell{Category: cat.Key, ChannelType: ch.ChannelType, Destination: ch.DestinationKey, Enabled: false})
			}
		}
	}
	return out
}
