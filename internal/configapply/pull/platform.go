// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"context"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func pullPlatformBlock(ctx context.Context, c *reconcile.Client, opts Options, res *Result) (*platformBlock, error) {
	if opts.OnlyKind != "" {
		return nil, nil
	}

	block := &platformBlock{}
	pp, err := pullProfileProviderBlock(ctx, c)
	if err != nil {
		return nil, err
	}
	block.ProfileProvider = pp
	dr, err := pullDefaultRolesBlock(ctx, c, res)
	if err != nil {
		return nil, err
	}
	block.DefaultRoles = dr
	ws, err := pullWebuiSettingsBlock(ctx, c)
	if err != nil {
		return nil, err
	}
	block.WebuiSettings = ws

	if block.ProfileProvider == nil && block.DefaultRoles == nil && block.WebuiSettings == nil {
		return nil, nil
	}
	return block, nil
}

func pullProfileProviderBlock(ctx context.Context, c *reconcile.Client) (*profileProviderBlock, error) {
	resp, err := c.GetProfileProvider(ctx)
	if err != nil {
		return nil, fmt.Errorf("get profile provider: %w", err)
	}
	if resp == nil || resp.ToolsetID == nil || *resp.ToolsetID == "" {
		return nil, nil
	}

	toolsets, err := c.ListToolsets(ctx)
	if err != nil {
		return nil, fmt.Errorf("list toolsets to resolve profile provider toolset name: %w", err)
	}
	var name string
	for _, ts := range toolsets {
		if ts.ID == *resp.ToolsetID {
			name = ts.Name
			break
		}
	}
	if name == "" {
		return nil, fmt.Errorf("platform.profileProvider: wired toolset ID %q is not present in the platform's toolset list — refusing to emit a manifest that won't round-trip", *resp.ToolsetID)
	}
	return &profileProviderBlock{
		Toolset: name,
		Claim:   resp.LookupKey,
	}, nil
}

func pullWebuiSettingsBlock(ctx context.Context, c *reconcile.Client) (*manifest.WebuiSettingsSpec, error) {
	resp, err := c.GetWebuiSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("get webui settings: %w", err)
	}
	if resp == nil {
		return nil, nil
	}
	block := &manifest.WebuiSettingsSpec{
		AppName:               resp.AppName,
		WelcomeMessage:        resp.WelcomeMessage,
		CollectFeedback:       resp.CollectFeedback,
		PublishFeedback:       resp.PublishFeedback,
		FeedbackTaskInfoLevel: resp.FeedbackTaskInfoLevel,
		DisclaimerText:        resp.DisclaimerText,
		LogoURL:               resp.LogoURL,
		SmallLogoURL:          resp.SmallLogoURL,
		SystemPurpose:         resp.SystemPurpose,
		ResponseFormat:        resp.ResponseFormat,
	}
	if !block.HasOverrides() {
		return nil, nil
	}
	return block, nil
}

func pullDefaultRolesBlock(ctx context.Context, c *reconcile.Client, res *Result) (*defaultRolesBlock, error) {
	resp, err := c.GetDefaultRoles(ctx)
	if err != nil {
		return nil, defaultRolesFetchErr(err, res)
	}
	if resp == nil || resp.Origin != entplatform.RBACOriginCustom || len(resp.RoleIDs) == 0 {
		return nil, nil
	}
	roles, err := c.ListRBACRoles(ctx)
	if err != nil {
		return nil, defaultRolesFetchErr(err, res)
	}
	idToName := roleIDToName(roles)
	names := make([]string, 0, len(resp.RoleIDs))
	for _, id := range resp.RoleIDs {
		if n, ok := idToName[id]; ok {
			names = append(names, n)
		} else {
			names = append(names, id)
		}
	}
	return &defaultRolesBlock{Roles: names}, nil
}

func defaultRolesFetchErr(err error, res *Result) error {
	if isFeatureDisabled(err) {
		res.Warnings = append(res.Warnings, fmt.Sprintf("skipping platform.defaultRoles: %s", err))
		return nil
	}
	return fmt.Errorf("get platform.defaultRoles: %w", err)
}
