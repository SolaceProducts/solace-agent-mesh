// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"fmt"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

var profileProviderResource = platformResource{path: "/profileProvider"}

type profileProviderVerb string

const (
	profileProviderVerbNone      profileProviderVerb = "none"
	profileProviderVerbPut       profileProviderVerb = "put"
	profileProviderVerbDelete    profileProviderVerb = "delete"
	profileProviderVerbUnchanged profileProviderVerb = "unchanged"
)

type profileProviderPlan struct {
	Verb         profileProviderVerb
	ToolsetID    string
	ToolsetName  string
	LookupKey    string
	CurrentState *wire.ProfileProviderResponse
}

type profileProviderOpResult struct {
	Verb profileProviderVerb
	Err  error
}

type profileProviderOutcome struct {
	Applied  *profileProviderOpResult
	Skipped  *profileProviderOpResult
	Failures []profileProviderOpResult
}

type toolsetLookup struct {
	byName        map[string]string
	desiredByName map[string]struct{}
}

func newToolsetLookup(actual []wire.ToolsetResponse, desired []string) *toolsetLookup {
	l := &toolsetLookup{
		byName:        make(map[string]string, len(actual)),
		desiredByName: make(map[string]struct{}, len(desired)),
	}
	for _, ts := range actual {
		if ts.Name != "" && ts.ID != "" {
			l.byName[ts.Name] = ts.ID
		}
	}
	for _, n := range desired {
		if n != "" {
			l.desiredByName[n] = struct{}{}
		}
	}
	return l
}

func (l *toolsetLookup) resolveToolsetID(name string) (string, error) {
	if id, ok := l.byName[name]; ok {
		return id, nil
	}
	if _, ok := l.desiredByName[name]; ok {
		return name, nil
	}
	return "", fmt.Errorf("platform.profileProvider.toolset: toolset %q is not declared in the manifest (under resources.toolsets) and does not exist on the platform", name)
}

func planProfileProvider(ctx context.Context, c *Client, desired *manifest.ProfileProviderSpec, lookup *toolsetLookup) (*profileProviderPlan, error) {
	current, err := getProfileProvider(ctx, c)
	if err != nil {
		return nil, err
	}

	if desired == nil {
		if current == nil || current.ToolsetID == nil || *current.ToolsetID == "" {
			return &profileProviderPlan{Verb: profileProviderVerbNone, CurrentState: current}, nil
		}
		return &profileProviderPlan{Verb: profileProviderVerbDelete, CurrentState: current}, nil
	}

	toolsetID, err := lookup.resolveToolsetID(desired.Toolset)
	if err != nil {
		return nil, err
	}

	claim := desired.Claim
	if current != nil && currentMatches(current, toolsetID, claim) {
		return &profileProviderPlan{
			Verb:         profileProviderVerbUnchanged,
			ToolsetID:    toolsetID,
			ToolsetName:  desired.Toolset,
			LookupKey:    claim,
			CurrentState: current,
		}, nil
	}

	return &profileProviderPlan{
		Verb:         profileProviderVerbPut,
		ToolsetID:    toolsetID,
		ToolsetName:  desired.Toolset,
		LookupKey:    claim,
		CurrentState: current,
	}, nil
}

func currentMatches(current *wire.ProfileProviderResponse, desiredToolsetID, desiredClaim string) bool {
	if current == nil {
		return false
	}
	if current.ToolsetID == nil {
		return false
	}
	if *current.ToolsetID != desiredToolsetID {
		return false
	}
	if desiredClaim == "" {
		return true
	}
	return current.LookupKey == desiredClaim
}

func getProfileProvider(ctx context.Context, c *Client) (*wire.ProfileProviderResponse, error) {
	return c.GetProfileProvider(ctx)
}

func (c *Client) GetProfileProvider(ctx context.Context) (*wire.ProfileProviderResponse, error) {
	var resp wire.ProfileProviderResponse
	if err := c.callResource(ctx, profileProviderResource, http.MethodGet, "", nil, &resp); err != nil {
		return nil, fmt.Errorf("get profile provider: %w", err)
	}
	return &resp, nil
}

func applyProfileProvider(ctx context.Context, c *Client, plan *profileProviderPlan, opts ApplyOptions) *profileProviderOutcome {
	if plan == nil || plan.Verb == profileProviderVerbNone {
		return nil
	}
	switch plan.Verb {
	case profileProviderVerbUnchanged:
		return &profileProviderOutcome{Skipped: &profileProviderOpResult{Verb: plan.Verb}}
	case profileProviderVerbPut:
		toolsetID, err := resolvePlaceholderProfileProviderToolsetID(ctx, c, plan)
		if err != nil {
			res := profileProviderOpResult{Verb: profileProviderVerbPut, Err: err}
			return &profileProviderOutcome{Failures: []profileProviderOpResult{res}}
		}
		body := wire.ReplaceProfileProviderRequest{ToolsetID: toolsetID, LookupKey: profileProviderLookupKey(ctx, c, plan)}
		err = c.callResource(ctx, profileProviderResource, http.MethodPut, "", body, nil)
		res := profileProviderOpResult{Verb: profileProviderVerbPut, Err: err}
		if err != nil {
			return &profileProviderOutcome{Failures: []profileProviderOpResult{res}}
		}
		return &profileProviderOutcome{Applied: &res}
	case profileProviderVerbDelete:
		if !opts.Prune {
			return &profileProviderOutcome{Skipped: &profileProviderOpResult{Verb: profileProviderVerbDelete}}
		}
		err := c.callResource(ctx, profileProviderResource, http.MethodDelete, "", nil, nil)
		res := profileProviderOpResult{Verb: profileProviderVerbDelete, Err: err}
		if err != nil {
			return &profileProviderOutcome{Failures: []profileProviderOpResult{res}}
		}
		return &profileProviderOutcome{Applied: &res}
	}
	return &profileProviderOutcome{Skipped: &profileProviderOpResult{Verb: profileProviderVerbNone}}
}

func profileProviderLookupKey(ctx context.Context, c *Client, plan *profileProviderPlan) string {
	if plan.LookupKey != "" || plan.CurrentState == nil {
		return plan.LookupKey
	}
	fresh, err := c.GetProfileProvider(ctx)
	if err != nil {
		reconcileLogger.Warn("profile provider re-read failed; the lookup key will be written from the plan-time snapshot and may revert a concurrent change",
			"error", err)
		return plan.CurrentState.LookupKey
	}
	return fresh.LookupKey
}

func resolvePlaceholderProfileProviderToolsetID(ctx context.Context, c *Client, plan *profileProviderPlan) (string, error) {
	if plan.ToolsetID != plan.ToolsetName {
		return plan.ToolsetID, nil
	}
	toolsets, err := c.ListToolsets(ctx)
	if err != nil {
		return "", fmt.Errorf("list toolsets to resolve profile provider sentinel: %w", err)
	}
	for _, ts := range toolsets {
		if ts.Name == plan.ToolsetName {
			return ts.ID, nil
		}
	}
	return "", fmt.Errorf("platform.profileProvider.toolset: toolset %q not found on platform after toolsets phase (expected the toolsets reconciler to have created it)", plan.ToolsetName)
}
