// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"context"
	"encoding/base64"
	stdmime "mime"
	"strings"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type EnrichParams struct {
	LookupValue string `json:"lookup_value" desc:"Resolved JWT claim value (e.g. lowercased email) identifying the user in the BambooHR directory."`
}

type SearchUsersParams struct {
	Query string `json:"query" desc:"Substring to match against display name, work email, or preferred name. Minimum length 2; shorter queries return an empty result."`
	Limit *int   `json:"limit,omitempty" desc:"Maximum number of profiles to return. Defaults to 10 when omitted or non-positive."`
}

type UserIDParam struct {
	UserID string `json:"user_id" desc:"Directory user identifier (lowercased work email)."`
}

type GetTimeOffParams struct {
	UserID  *string  `json:"user_id,omitempty" desc:"Directory user identifier (lowercased work email). For several people, use user_ids instead."`
	UserIDs []string `json:"user_ids,omitempty" desc:"Directory user identifiers (lowercased work emails) to look up in a single call. Prefer this over calling the tool once per person."`
	Start   *string  `json:"start" desc:"Inclusive ISO-8601 start date (YYYY-MM-DD). Empty means today."`
	End     *string  `json:"end" desc:"Inclusive ISO-8601 end date (YYYY-MM-DD). Empty means today + 30 days."`
}

const (
	enrichAuthenticatedUserInstructions = `Resolves an authenticated user's lookup value (typically their email) to a BambooHR profile.

This tool is called by the platform identity service. The caller passes the single resolved claim value configured by 'lookup_key' (default: 'email'). Returns the canonical Profile shape: lowercased email as id, supervisor-by-email, hire date as ISO-8601.`

	searchUsersInstructions = `Searches the BambooHR employee directory for users matching a substring against display name, work email, or preferred name. Used by the @mention UI for type-ahead search.`

	getUserProfileInstructions = `Returns the raw BambooHR directory record for a user, keyed by lowercased work email. The shape is the vendor-specific record (not the canonical Profile), so agent tools can render fields such as photoUrl.`

	getTimeOffInstructions = `Returns time-off entries for one or more users in the date range. To look up a team or several people, pass them all in a single call via 'user_ids' (a list of lowercased work emails) rather than calling this tool once per person; a single user may use 'user_id'. Day-granularity records with type (e.g. "Vacation") and start/end ISO dates. Soft-fails to empty entries if the BambooHR time-off endpoint returns an error. With 'user_ids', entries are grouped per user under 'results'; a single 'user_id' returns a flat 'entries' list.`

	getUserProfilePictureInstructions = `Returns the user's BambooHR profile photo. The photo is saved as a viewable image artifact named "profile_picture.<ext>" (present it to the user directly); a base64-encoded copy plus its MIME type is also included for non-agent callers. Empty result when the user has no photo. Soft-fails on photo-fetch errors.`
)

func enrichAuthenticatedUser(ctx context.Context, p EnrichParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	pr, errMsg := buildProvider(tc)
	if errMsg != "" {
		return sdk.Error(errMsg), nil
	}
	profile, err := pr.enrichByLookupValue(ctx, p.LookupValue)
	if err != nil {
		return sdk.Error("BambooHR lookup failed: " + err.Error()), nil
	}
	if profile == nil {
		return sdk.OK("No profile found for the supplied lookup value.",
			sdk.WithData(map[string]any{"profile": nil}),
		), nil
	}
	return sdk.OK("Profile resolved.",
		sdk.WithData(map[string]any{"profile": profile}),
	), nil
}

func searchUsers(ctx context.Context, p SearchUsersParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	pr, errMsg := buildProvider(tc)
	if errMsg != "" {
		return sdk.Error(errMsg), nil
	}
	limit := 10
	if p.Limit != nil {
		limit = *p.Limit
	}
	results, err := pr.searchUsers(ctx, p.Query, limit)
	if err != nil {
		return sdk.Error("BambooHR search failed: " + err.Error()), nil
	}
	return sdk.OK("Search complete.",
		sdk.WithData(map[string]any{
			"profiles": results,
			"count":    len(results),
		}),
	), nil
}

func getUserProfile(ctx context.Context, p UserIDParam, tc *sdk.ToolContext) (*sdk.Result, error) {
	pr, errMsg := buildProvider(tc)
	if errMsg != "" {
		return sdk.Error(errMsg), nil
	}
	rec, err := pr.getUserProfile(ctx, p.UserID)
	if err != nil {
		return sdk.Error("BambooHR profile fetch failed: " + err.Error()), nil
	}
	if rec == nil {
		return sdk.OK("No profile found.",
			sdk.WithData(map[string]any{"profile": nil}),
		), nil
	}
	return sdk.OK("Profile retrieved.",
		sdk.WithData(map[string]any{"profile": rec}),
	), nil
}

func getTimeOff(ctx context.Context, p GetTimeOffParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	pr, errMsg := buildProvider(tc)
	if errMsg != "" {
		return sdk.Error(errMsg), nil
	}

	single := ""
	if p.UserID != nil {
		single = *p.UserID
	}
	userIDs := mergeUserIDs(single, p.UserIDs)
	if len(userIDs) == 0 {
		return sdk.Error("get_time_off requires user_id or user_ids."), nil
	}

	if len(userIDs) == 1 {
		entries, err := pr.getTimeOff(ctx, userIDs[0], derefString(p.Start), derefString(p.End))
		if err != nil {
			return sdk.Error("BambooHR time-off fetch failed: " + err.Error()), nil
		}
		if entries == nil {
			entries = []TimeOffEntry{}
		}
		return sdk.OK("Time-off entries retrieved.",
			sdk.WithData(map[string]any{
				"entries": entries,
				"count":   len(entries),
			}),
		), nil
	}

	results, err := pr.getTimeOffMulti(ctx, userIDs, derefString(p.Start), derefString(p.End))
	if err != nil {
		return sdk.Error("BambooHR time-off fetch failed: " + err.Error()), nil
	}
	total := 0
	for _, r := range results {
		total += len(r.Entries)
	}
	return sdk.OK("Time-off entries retrieved.",
		sdk.WithData(map[string]any{
			"results": results,
			"count":   total,
		}),
	), nil
}

func mergeUserIDs(single string, list []string) []string {
	seen := make(map[string]struct{}, len(list)+1)
	out := make([]string, 0, len(list)+1)
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		key := strings.ToLower(id)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, id)
	}
	add(single)
	for _, id := range list {
		add(id)
	}
	return out
}

func getUserProfilePicture(ctx context.Context, p UserIDParam, tc *sdk.ToolContext) (*sdk.Result, error) {
	pr, errMsg := buildProvider(tc)
	if errMsg != "" {
		return sdk.Error(errMsg), nil
	}
	body, contentType, err := pr.getUserProfilePicture(ctx, p.UserID)
	if err != nil {
		return sdk.Error("BambooHR photo fetch failed: " + err.Error()), nil
	}
	if body == nil {
		return sdk.OK("No profile picture available.",
			sdk.WithData(map[string]any{
				"image_base64": "",
				"mime_type":    "",
			}),
		), nil
	}
	return sdk.OK("Profile picture retrieved.",
		sdk.WithData(map[string]any{
			"image_base64": base64.StdEncoding.EncodeToString(body),
			"mime_type":    contentType,
		}),
		sdk.WithDataObjects(sdk.DataObject{
			Name:        "profile_picture" + imageExtForMIME(contentType),
			Content:     body,
			MIMEType:    contentType,
			Disposition: sdk.DispositionArtifact,
		}),
	), nil
}

func imageExtForMIME(mime string) string {
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	}
	if strings.HasPrefix(mime, "image/") {
		if exts, _ := stdmime.ExtensionsByType(mime); len(exts) > 0 {
			return exts[0]
		}
	}
	return ".jpg"
}

func buildProvider(tc *sdk.ToolContext) (*provider, string) {
	cfg, errMsg := loadConfig(tc)
	if errMsg != "" {
		return nil, errMsg
	}
	return newProvider(cfg, nil), ""
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
