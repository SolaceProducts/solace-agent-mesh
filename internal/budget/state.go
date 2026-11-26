// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budget

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

type EnforcementState struct {
	BudgetsInUse        bool              `json:"budgetsInUse"`
	MeshBlocked         bool              `json:"meshBlocked"`
	BlockedUsers        []string          `json:"blockedUsers,omitempty"`
	DefaultUsersBlocked bool              `json:"defaultUsersBlocked,omitempty"`
	AdmittedUsers       []string          `json:"admittedUsers,omitempty"`
	WarnedUsers         []string          `json:"warnedUsers,omitempty"`
	DefaultUsersWarned  bool              `json:"defaultUsersWarned,omitempty"`
	UnwarnedUsers       []string          `json:"unwarnedUsers,omitempty"`
	WarnCapKeys         map[string]string `json:"warnCapKeys,omitempty"`
	DefaultWarnCapKey   string            `json:"defaultWarnCapKey,omitempty"`
	WarningsOmitted     bool              `json:"warningsOmitted,omitempty"`
	UsersOmitted        bool              `json:"usersOmitted,omitempty"`
	PeriodEnd           time.Time         `json:"periodEnd"`
	ComputedAt          time.Time         `json:"computedAt"`
}

func DecodeState(data []byte) (EnforcementState, error) {
	var state EnforcementState
	if err := json.Unmarshal(data, &state); err != nil {
		return EnforcementState{}, fmt.Errorf("decode budget state: %w", err)
	}
	if err := state.Validate(); err != nil {
		return EnforcementState{}, err
	}
	return state, nil
}

func (s EnforcementState) Validate() error {
	switch {
	case s.ComputedAt.IsZero():
		return errors.New("budget state has no computedAt")
	case s.MeshBlocked && !s.BudgetsInUse:
		return errors.New("budget state blocks the mesh with no budget in use")
	case s.MeshBlocked && s.PeriodEnd.IsZero():
		return errors.New("budget state blocks the mesh with no periodEnd")
	case s.UsersOmitted && (s.listsRefusals() || s.listsWarnings()):
		return errors.New("budget state omits its users but lists some")
	case s.WarningsOmitted && s.listsWarnings():
		return errors.New("budget state omits its warnings but lists some")
	}
	return nil
}

func (s EnforcementState) listsRefusals() bool {
	return len(s.BlockedUsers) > 0 || len(s.AdmittedUsers) > 0
}

func (s EnforcementState) listsWarnings() bool {
	return len(s.WarnedUsers) > 0 || len(s.UnwarnedUsers) > 0 || len(s.WarnCapKeys) > 0
}

func (s EnforcementState) WithoutWarnings() EnforcementState {
	s.WarnedUsers, s.UnwarnedUsers, s.WarnCapKeys, s.WarningsOmitted = nil, nil, nil, true
	return s
}

func (s EnforcementState) WithoutUsers() EnforcementState {
	s = s.WithoutWarnings()
	s.BlockedUsers, s.AdmittedUsers, s.UsersOmitted, s.WarningsOmitted = nil, nil, true, false
	return s
}

func (s EnforcementState) WithHeldUsers(held *EnforcementState) EnforcementState {
	samePeriod := held != nil && held.PeriodEnd.Equal(s.PeriodEnd)
	if s.UsersOmitted {
		if samePeriod && held.DefaultUsersBlocked == s.DefaultUsersBlocked {
			s.BlockedUsers, s.AdmittedUsers = held.BlockedUsers, held.AdmittedUsers
		} else {
			s.DefaultUsersBlocked = false
		}
		s.UsersOmitted, s.WarningsOmitted = false, true
	}
	if s.WarningsOmitted {
		if samePeriod && held.DefaultUsersWarned == s.DefaultUsersWarned {
			s.WarnedUsers, s.UnwarnedUsers, s.WarnCapKeys = held.WarnedUsers, held.UnwarnedUsers, held.WarnCapKeys
		} else {
			s.DefaultUsersWarned = false
		}
		s.WarningsOmitted = false
	}
	return s
}

func (s EnforcementState) SameDecision(other EnforcementState) bool {
	return s.SameHeadline(other) &&
		slices.Equal(s.BlockedUsers, other.BlockedUsers) &&
		slices.Equal(s.AdmittedUsers, other.AdmittedUsers) &&
		slices.Equal(s.WarnedUsers, other.WarnedUsers) &&
		slices.Equal(s.UnwarnedUsers, other.UnwarnedUsers) &&
		maps.Equal(s.WarnCapKeys, other.WarnCapKeys)
}

func (s EnforcementState) SameHeadline(other EnforcementState) bool {
	return s.BudgetsInUse == other.BudgetsInUse &&
		s.MeshBlocked == other.MeshBlocked &&
		s.PeriodEnd.Equal(other.PeriodEnd) &&
		s.DefaultUsersBlocked == other.DefaultUsersBlocked &&
		s.DefaultUsersWarned == other.DefaultUsersWarned &&
		s.DefaultWarnCapKey == other.DefaultWarnCapKey
}

func (s EnforcementState) AnyBlocked() bool {
	return s.MeshBlocked || s.DefaultUsersBlocked || len(s.BlockedUsers) > 0
}

func (s EnforcementState) UserBlocked(key string) bool {
	if s.DefaultUsersBlocked {
		_, admitted := slices.BinarySearch(s.AdmittedUsers, key)
		return !admitted
	}
	_, blocked := slices.BinarySearch(s.BlockedUsers, key)
	return blocked
}

func (s EnforcementState) UserLevel(key string) Level {
	switch {
	case s.UserBlocked(key):
		return LevelLimit
	case s.userWarned(key):
		return LevelWarn
	default:
		return LevelNone
	}
}

func (s EnforcementState) WarnCapKey(key string) string {
	if s.UserLevel(key) != LevelWarn {
		return ""
	}
	if capKey, ok := s.WarnCapKeys[key]; ok {
		return capKey
	}
	return s.DefaultWarnCapKey
}

func (s EnforcementState) userWarned(key string) bool {
	if s.DefaultUsersWarned {
		_, unwarned := slices.BinarySearch(s.UnwarnedUsers, key)
		return !unwarned
	}
	_, warned := slices.BinarySearch(s.WarnedUsers, key)
	return warned
}

const (
	userKeyIDPrefix  = "id:"
	userKeySubPrefix = "sub:"
)

func UserKey(id, sub string) string {
	switch {
	case id != "":
		return userKeyIDPrefix + strings.ToLower(id)
	case sub != "":
		sum := sha256.Sum256([]byte(sub))
		return userKeySubPrefix + hex.EncodeToString(sum[:])
	default:
		return ""
	}
}

func (s EnforcementState) UserListAttrs() []any {
	blockedUsers, blockedIdentities := countUserKeys(s.BlockedUsers)
	admittedUsers, admittedIdentities := countUserKeys(s.AdmittedUsers)
	return []any{
		"blockedUsers", blockedUsers, "blockedIdentities", blockedIdentities,
		"defaultUsersBlocked", s.DefaultUsersBlocked,
		"admittedUsers", admittedUsers, "admittedIdentities", admittedIdentities,
	}
}

func countUserKeys(keys []string) (users, identities int) {
	for _, key := range keys {
		if strings.HasPrefix(key, userKeyIDPrefix) {
			users++
		} else {
			identities++
		}
	}
	return users, identities
}

func Exhausted(spent int64, amount *int64, topUp int64) bool {
	limit := EffectiveCap(amount, topUp)
	return limit != nil && spent >= *limit
}
