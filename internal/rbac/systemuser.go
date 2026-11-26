// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import (
	_ "embed"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed defaults/system_defaults.yaml
var systemDefaultsYAML []byte

const (
	SystemUserIssuer = "sam:system"

	SystemSubPrefix = "system:"

	DefaultSystemUserSub = "system:default"

	DefaultSystemUserID = "6dbe83db-3549-4d99-b4fb-c3aea7b75219"

	RoleSystemInvokeAll = "system_agent_invoke"

	ChannelSystemUserSub = "system:channel"

	ChannelSystemUserID = "1f8ffb5d-fc1b-47b3-8578-559670c859ae"

	ChannelSystemUserRunAs = "channel"

	RoleSystemChannelInvoke = "system_channel_invoke"

	EvalSystemUserSub = "system:eval"

	EvalSystemUserID = "a38ff022-96f3-4a78-a2e5-8a71bfa882ab"

	EvalSystemUserRunAs = "eval"

	RoleSystemEvalInvoke = "system_eval_invoke"

	EvidenceSystemUserSub = "system:evidence"

	EvidenceSystemUserID = "6f919ea1-0bcc-45d7-825a-f6fdbb388c1e"

	ProvisionedViaSystem = "system"
)

const (
	ServiceAccountRenameRemovalDate = "2027-12-01"
	ServiceAccountRenameNoticeURL   = "https://docs.solace.com/Agent-Mesh/Framework/reference/deprecations.htm#system-users-renamed-to-service-accounts"
)

func SystemUserPrincipal(name string) string {
	if name == "" {
		return DefaultSystemUserSub
	}
	if strings.HasPrefix(name, SystemSubPrefix) {
		return name
	}
	return SystemSubPrefix + name
}

func SystemUserName(sub string) string {
	return strings.TrimPrefix(sub, SystemSubPrefix)
}

func IsSystemPrincipal(userID string) bool {
	return strings.HasPrefix(userID, SystemSubPrefix)
}

type ReservedSystemUser struct {
	Sub                    string
	ID                     string
	DisplayName            string
	HiddenFromRunAsPicker  bool
	HiddenFromUserListings bool
}

var reservedSystemUsers = []ReservedSystemUser{
	{Sub: DefaultSystemUserSub, ID: DefaultSystemUserID, DisplayName: "Default"},
	{Sub: ChannelSystemUserSub, ID: ChannelSystemUserID, DisplayName: "Channel", HiddenFromRunAsPicker: true, HiddenFromUserListings: true},
	{Sub: EvalSystemUserSub, ID: EvalSystemUserID, DisplayName: "Eval", HiddenFromRunAsPicker: true},
	{Sub: EvidenceSystemUserSub, ID: EvidenceSystemUserID, DisplayName: "Evidence", HiddenFromRunAsPicker: true},
}

func ReservedSystemUsers() []ReservedSystemUser {
	return slices.Clone(reservedSystemUsers)
}

func ReservedSystemUserSubs() []string {
	subs := make([]string, 0, len(reservedSystemUsers))
	for _, u := range reservedSystemUsers {
		subs = append(subs, u.Sub)
	}
	return subs
}

func UserListingHiddenSubs() []string {
	var subs []string
	for _, u := range reservedSystemUsers {
		if u.HiddenFromUserListings {
			subs = append(subs, u.Sub)
		}
	}
	return subs
}

func UserListingHiddenIDs() []string {
	var ids []string
	for _, u := range reservedSystemUsers {
		if u.HiddenFromUserListings {
			ids = append(ids, u.ID)
		}
	}
	return ids
}

func LookupReservedSystemUser(sub string) (ReservedSystemUser, bool) {
	for _, u := range reservedSystemUsers {
		if u.Sub == sub {
			return u, true
		}
	}
	return ReservedSystemUser{}, false
}

func ReservedSystemUserID(principal string) string {
	u, _ := LookupReservedSystemUser(principal)
	return u.ID
}

func IsReservedSystemUserSub(sub string) bool {
	_, ok := LookupReservedSystemUser(sub)
	return ok
}

func IsNonDelegatablePrincipal(userID string) bool {
	return IsSystemPrincipal(userID)
}

func EmbeddedDefaults() (RolesConfig, UsersConfig, error) {
	var doc struct {
		Roles map[string]RoleDefinition `yaml:"roles"`
		Users map[string]UserAssignment `yaml:"users"`
	}
	if err := yaml.Unmarshal(systemDefaultsYAML, &doc); err != nil {
		return RolesConfig{}, UsersConfig{}, fmt.Errorf("rbac embedded defaults: %w", err)
	}
	return RolesConfig{Roles: doc.Roles}, UsersConfig{Users: doc.Users}, nil
}
