// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type devUser struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	Name       string `json:"name"`
	Title      string `json:"title"`
	Department string `json:"department"`
}

type profile struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName,omitempty"`
	WorkEmail   string `json:"workEmail,omitempty"`
	JobTitle    string `json:"jobTitle,omitempty"`
	Department  string `json:"department,omitempty"`
}

type enrichParams struct {
	LookupValue string `json:"lookup_value" desc:"Resolved JWT claim value (typically lowercased email) identifying the user."`
}

type searchParams struct {
	Query string `json:"query" desc:"Substring to match against id / email / name."`
	Limit *int   `json:"limit,omitempty" desc:"Maximum number of profiles to return. Defaults to 10."`
}

type userIDParams struct {
	UserID string `json:"user_id" desc:"Directory user identifier (typically lowercased email)."`
}

var configSchema = []sdk.ConfigSchemaField{
	{Key: "users_file", Type: "string", Description: "Path to a JSON file containing the dev user fixture array.", Required: true},
}

func main() {
	sdk.Run(
		sdk.NewTool("enrich_authenticated_user",
			"Resolves the authenticated user's lookup value (email) to a canonical Profile sourced from the dev users JSON file.",
			enrichAuthenticatedUser,
			sdk.WithConfigSchema(configSchema...),
			sdk.WithEffect(sdk.EffectReadOnly),
		),
		sdk.NewTool("search_users",
			"Searches the dev users fixture for a substring match against id / email / name.",
			searchUsers,
			sdk.WithConfigSchema(configSchema...),
			sdk.WithEffect(sdk.EffectReadOnly),
		),
		sdk.NewTool("get_user_profile",
			"Returns the raw dev user record keyed by id (email).",
			getUserProfile,
			sdk.WithConfigSchema(configSchema...),
			sdk.WithEffect(sdk.EffectReadOnly),
		),
	)
}

func enrichAuthenticatedUser(_ context.Context, p enrichParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	users, errMsg := loadUsers(tc)
	if errMsg != "" {
		return sdk.Error(errMsg), nil
	}
	u := findUser(users, p.LookupValue)
	if u == nil {
		return sdk.OK("No profile found.", sdk.WithData(map[string]any{"profile": nil})), nil
	}
	return sdk.OK("Profile resolved.", sdk.WithData(map[string]any{"profile": toProfile(u)})), nil
}

func searchUsers(_ context.Context, p searchParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	users, errMsg := loadUsers(tc)
	if errMsg != "" {
		return sdk.Error(errMsg), nil
	}
	limit := 10
	if p.Limit != nil && *p.Limit > 0 {
		limit = *p.Limit
	}
	q := strings.ToLower(strings.TrimSpace(p.Query))
	var matches []*profile
	for i := range users {
		u := &users[i]
		hay := strings.ToLower(u.ID + " " + u.Email + " " + u.Name)
		if q != "" && !strings.Contains(hay, q) {
			continue
		}
		matches = append(matches, toProfile(u))
		if len(matches) >= limit {
			break
		}
	}
	return sdk.OK("Search complete.", sdk.WithData(map[string]any{
		"profiles": matches,
		"count":    len(matches),
	})), nil
}

func getUserProfile(_ context.Context, p userIDParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	users, errMsg := loadUsers(tc)
	if errMsg != "" {
		return sdk.Error(errMsg), nil
	}
	u := findUser(users, p.UserID)
	if u == nil {
		return sdk.OK("No profile found.", sdk.WithData(map[string]any{"profile": nil})), nil
	}
	return sdk.OK("Profile retrieved.", sdk.WithData(map[string]any{
		"profile": map[string]any{
			"id":         u.ID,
			"email":      u.Email,
			"name":       u.Name,
			"title":      u.Title,
			"department": u.Department,
		},
	})), nil
}

func findUser(users []devUser, value string) *devUser {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return nil
	}
	for i := range users {
		u := &users[i]
		if strings.EqualFold(u.ID, value) || strings.EqualFold(u.Email, value) {
			return u
		}
	}
	return nil
}

func toProfile(u *devUser) *profile {
	return &profile{
		ID:          strings.ToLower(u.ID),
		DisplayName: u.Name,
		WorkEmail:   strings.ToLower(u.Email),
		JobTitle:    u.Title,
		Department:  u.Department,
	}
}

var (
	usersCacheMu sync.Mutex
	usersCache   = map[string][]devUser{}
)

func loadUsers(tc *sdk.ToolContext) ([]devUser, string) {
	path := strings.TrimSpace(tc.GetConfigString("users_file", ""))
	if path == "" {
		return nil, "users_file is required in tool config."
	}
	usersCacheMu.Lock()
	cached, ok := usersCache[path]
	usersCacheMu.Unlock()
	if ok {
		return cached, ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "failed to read users_file: " + err.Error()
	}
	var users []devUser
	if err := json.Unmarshal(data, &users); err != nil {
		return nil, "failed to parse users_file: " + err.Error()
	}
	if len(users) == 0 {
		return nil, "users_file contained no users"
	}
	usersCacheMu.Lock()
	usersCache[path] = users
	usersCacheMu.Unlock()
	return users, ""
}
