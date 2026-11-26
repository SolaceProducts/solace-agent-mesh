// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/webuisettings"
)

func (e *GatewayExecutor) webUICore() *GatewayCore {
	for _, core := range e.Gateways() {
		if core != nil && core.ServesWebUI() {
			return core
		}
	}
	return nil
}

type webuiSettingsFieldResponse struct {
	Value  any    `json:"value"`
	Source string `json:"source"`
}

func (e *GatewayExecutor) handleWebuiSettingsInspect(ctx context.Context, method string, identity *auth.Identity) (any, error) {
	if method != "get" {
		return nil, fmt.Errorf("webuiSettings: unsupported: %s", method)
	}
	if err := requireIdentityScope(identity, rbac.ScopeWebuiSettingsRead, e.controlAuthzEnabled()); err != nil {
		return nil, err
	}

	core := e.webUICore()
	if core == nil {
		return nil, fmt.Errorf("webuiSettings unavailable: no gateway instance in this process serves the web UI")
	}
	reg := core.WebuiSettingsRegistry()
	if reg == nil {
		return nil, fmt.Errorf("webuiSettings unavailable: no settings registry configured for this gateway")
	}
	row, err := reg.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve webui settings: %w", err)
	}

	eff := webuisettings.Resolve(row, WebuiSettingsFallbackFromConfig(core.Config().FrontendConfigRaw, core.Config().FeedbackPublishingRaw))
	assistantEff := core.ResolveAssistantContext(ctx)

	return map[string]webuiSettingsFieldResponse{
		"appName":               {Value: eff.AppName, Source: string(eff.Sources["appName"])},
		"welcomeMessage":        {Value: eff.WelcomeMessage, Source: string(eff.Sources["welcomeMessage"])},
		"collectFeedback":       {Value: eff.CollectFeedback, Source: string(eff.Sources["collectFeedback"])},
		"publishFeedback":       {Value: eff.PublishFeedback, Source: string(eff.Sources["publishFeedback"])},
		"feedbackTaskInfoLevel": {Value: eff.FeedbackTaskInfoLevel, Source: string(eff.Sources["feedbackTaskInfoLevel"])},
		"disclaimerText":        {Value: eff.DisclaimerText, Source: string(eff.Sources["disclaimerText"])},
		"logoUrl":               {Value: eff.LogoURL, Source: string(eff.Sources["logoUrl"])},
		"smallLogoUrl":          {Value: eff.SmallLogoURL, Source: string(eff.Sources["smallLogoUrl"])},
		"systemPurpose":         {Value: assistantEff.SystemPurpose, Source: string(assistantEff.Sources["systemPurpose"])},
		"responseFormat":        {Value: assistantEff.ResponseFormat, Source: string(assistantEff.Sources["responseFormat"])},
	}, nil
}
