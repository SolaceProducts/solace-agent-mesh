// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import (
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type RoleDefinition struct {
	Description string   `yaml:"description"`
	Scopes      []string `yaml:"scopes"`
	Inherits    []string `yaml:"inherits"`
}

type RolesConfig struct {
	Roles map[string]RoleDefinition `yaml:"roles"`
}

type UserAssignment struct {
	Roles []string `yaml:"roles"`
}

type UsersConfig struct {
	Users map[string]UserAssignment `yaml:"users"`
}

type RoleEngine struct {
	roles        map[string]RoleDefinition
	users        map[string]UserAssignment
	defaultRoles []string
}

func NewRoleEngine(roles RolesConfig, users UsersConfig, defaultRoles []string) (*RoleEngine, error) {
	if roles.Roles == nil {
		roles.Roles = make(map[string]RoleDefinition)
	}
	if users.Users == nil {
		users.Users = make(map[string]UserAssignment)
	}

	for name, role := range roles.Roles {
		for _, parent := range role.Inherits {
			if _, ok := roles.Roles[parent]; !ok {
				return nil, fmt.Errorf("role %q inherits from unknown role %q", name, parent)
			}
		}
	}
	for userID, assignment := range users.Users {
		for _, roleName := range assignment.Roles {
			if _, ok := roles.Roles[roleName]; !ok {
				return nil, fmt.Errorf("user %q references unknown role %q", userID, roleName)
			}
		}
	}
	for _, roleName := range defaultRoles {
		if _, ok := roles.Roles[roleName]; !ok {
			return nil, fmt.Errorf("default role %q not found in role definitions", roleName)
		}
	}

	graph := make(map[string][]string, len(roles.Roles))
	for name, role := range roles.Roles {
		graph[name] = role.Inherits
	}
	if cycle := detectCycles(graph); cycle != nil {
		return nil, fmt.Errorf("role inheritance cycle detected: %s", strings.Join(cycle, " -> "))
	}

	return &RoleEngine{
		roles:        roles.Roles,
		users:        users.Users,
		defaultRoles: defaultRoles,
	}, nil
}

func detectCycles(graph map[string][]string) []string {
	const (
		unvisited = 0
		inStack   = 1
		done      = 2
	)
	state := make(map[string]int, len(graph))
	path := make([]string, 0, len(graph))

	var dfs func(node string) []string
	dfs = func(node string) []string {
		state[node] = inStack
		path = append(path, node)

		for _, child := range graph[node] {
			switch state[child] {
			case inStack:
				for i, n := range path {
					if n == child {
						cycle := make([]string, len(path)-i+1)
						copy(cycle, path[i:])
						cycle[len(cycle)-1] = child
						return cycle
					}
				}
			case unvisited:
				if cycle := dfs(child); cycle != nil {
					return cycle
				}
			}
		}

		path = path[:len(path)-1]
		state[node] = done
		return nil
	}

	for node := range graph {
		if state[node] == unvisited {
			if cycle := dfs(node); cycle != nil {
				return cycle
			}
		}
	}
	return nil
}

func (e *RoleEngine) ResolveUserScopes(userID string) []string {
	assignment, ok := e.users[userID]
	if !ok {
		return e.ResolveRoleScopes(e.defaultRoles)
	}
	return e.ResolveRoleScopes(assignment.Roles)
}

func (e *RoleEngine) HasRole(roleName string) bool {
	_, ok := e.roles[roleName]
	return ok
}

func (e *RoleEngine) ResolveRoleScopes(roleNames []string) []string {
	seen := make(map[string]bool)
	var scopes []string

	for _, name := range roleNames {
		e.collectScopes(name, seen, make(map[string]bool), &scopes)
	}
	return scopes
}

func (e *RoleEngine) collectScopes(roleName string, seen map[string]bool, visited map[string]bool, scopes *[]string) {
	if visited[roleName] {
		return
	}
	visited[roleName] = true

	role, ok := e.roles[roleName]
	if !ok {
		return
	}

	for _, scope := range role.Scopes {
		if !seen[scope] {
			seen[scope] = true
			*scopes = append(*scopes, scope)
		}
	}

	for _, parent := range role.Inherits {
		e.collectScopes(parent, seen, visited, scopes)
	}
}

func LoadRolesFromFile(path string) (RolesConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RolesConfig{}, fmt.Errorf("reading roles file: %w", err)
	}
	var cfg RolesConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return RolesConfig{}, fmt.Errorf("parsing roles file: %w", err)
	}
	return cfg, nil
}

const msgUserIdentityNotCanonical = "rbac YAML user identity key is email-shaped but not already lowercase; it will only resolve via a case-insensitive fallback match"

const msgUserIdentityHasWhitespace = "rbac YAML user identity key is email-shaped but has leading or trailing whitespace; it will only resolve via a case-insensitive fallback match"

const msgUserIdentityFoldCollision = "rbac YAML user identities collide after case-insensitive folding; only one of them is reachable via the fallback match"

func LoadUsersFromFile(path string, logger *slog.Logger) (UsersConfig, error) {
	if logger == nil {
		logger = slog.Default()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return UsersConfig{}, fmt.Errorf("reading users file: %w", err)
	}
	var cfg UsersConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return UsersConfig{}, fmt.Errorf("parsing users file: %w", err)
	}
	warnNonCanonicalUserKeys(cfg.Users, path, logger)
	return cfg, nil
}

func warnNonCanonicalUserKeys(users map[string]UserAssignment, path string, logger *slog.Logger) {
	warnNonCanonicalBucket(users, path, logger, isFoldableEmailKey)
	warnNonCanonicalBucket(users, path, logger, isFoldableCompositeKey)
}

const maxLoggedIdentityLen = 128

func warnNonCanonicalBucket(users map[string]UserAssignment, path string, logger *slog.Logger, inBucket func(string) bool) {
	keys := make([]string, 0, len(users))
	for key := range users {
		if inBucket(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	variants := make(map[string][]string, len(keys))
	for _, key := range keys {
		folded := foldKey(key)
		variants[folded] = append(variants[folded], key)
		logged := key
		if len(logged) > maxLoggedIdentityLen {
			logged = logged[:maxLoggedIdentityLen] + "..."
		}
		if strings.ToLower(key) != key {
			logger.Warn(msgUserIdentityNotCanonical, "path", path, "configuredIdentity", logged)
		}
		if strings.TrimSpace(key) != key {
			logger.Warn(msgUserIdentityHasWhitespace, "path", path, "configuredIdentity", logged)
		}
	}

	lowers := make([]string, 0, len(variants))
	for folded := range variants {
		lowers = append(lowers, folded)
	}
	sort.Strings(lowers)
	for _, lower := range lowers {
		if len(variants[lower]) > 1 {
			logger.Warn(msgUserIdentityFoldCollision, "path", path, "variantCount", len(variants[lower]))
		}
	}
}

func isFoldableEmailKey(key string) bool {
	key = strings.TrimSpace(key)
	if IsSystemPrincipal(key) || strings.HasPrefix(key, deprecatedGatewayPrefix) {
		return false
	}
	return strings.Contains(key, "@")
}
