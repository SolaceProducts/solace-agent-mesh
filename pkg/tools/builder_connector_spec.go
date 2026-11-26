// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

var uploadedSpecPathPrefixes = [...]string{"/api/v1/platform/connectors/", "/api/v2/platform/connectors/"}

const missingOpenAPISpecMessage = "an OpenAPI connector needs values.specification_url set to the http(s) URL of its OpenAPI document; " +
	"the Builder cannot upload a spec file and inline specs are not supported, so without it the connector deploys with no tools. " +
	"Never invent or guess a spec URL, and never use a placeholder for it. If the user has not given one, stop and report back: " +
	"the lead Builder asks the user for the URL, or has them create the connector on the Connectors page with an uploaded spec " +
	"and reuses it as an existing platform component (origin: platform)"

const foreignUploadedSpecMessage = "values.specification_url is the path of a spec uploaded on the Connectors page, which only works on the " +
	"connector it was uploaded to, and no existing connector with this name has an uploaded spec. " + missingOpenAPISpecMessage

const existingWithoutUploadMessage = "a connector with this name already exists and has no spec uploaded on the Connectors page; " +
	"keep its stored values.specification_url (read it with platform_get_component_config) or set a new http(s) URL"

const ConnectorLookupPath = "connectors/lookup/byName"

var errConnectorLookupUnavailable = errors.New("the platform connector lookup is not available to this tool")

type ConnectorLookup struct {
	Exists       bool   `json:"exists"`
	Type         string `json:"type,omitempty"`
	HasResource  bool   `json:"hasResource"`
	HasAllowList bool   `json:"hasAllowList"`
}

func IsUploadedSpecPath(u string) bool {
	for _, prefix := range uploadedSpecPathPrefixes {
		if strings.HasPrefix(u, prefix) {
			return true
		}
	}
	return false
}

func isExternalSpecURL(raw string) bool {
	if !strings.HasPrefix(raw, "https://") && !strings.HasPrefix(raw, "http://") {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Host != ""
}

func openAPISpecProblem(ctx context.Context, toolCtx *tool.Context, cfg map[string]any) string {
	connectorType, _ := cfg["type"].(string)
	subtype, _ := cfg["subtype"].(string)
	if connectorType != "api" || subtype != "openapi" {
		return ""
	}
	values, _ := cfg["values"].(map[string]any)
	specURL, _ := values["specification_url"].(string)
	if isExternalSpecURL(specURL) {
		return ""
	}
	if _, ok := values["spec_url"]; ok && specURL == "" {
		return "values.spec_url is not a connector field; use values.specification_url"
	}
	if specURL != "" && !IsUploadedSpecPath(specURL) {
		return missingOpenAPISpecMessage
	}
	name, _ := cfg["name"].(string)
	if strings.TrimSpace(name) == "" {
		return missingOpenAPISpecMessage
	}
	if toolCtx == nil || toolCtx.ControlQuerier == nil {
		return fmt.Sprintf("could not check whether connector %q already has a spec uploaded on the Connectors page (%v); %s",
			name, errConnectorLookupUnavailable, missingOpenAPISpecMessage)
	}
	existing, err := LookupConnectorByName(ctx, toolCtx.ControlQuerier, name)
	if err != nil {
		slog.Default().With("logger_name", "builder-connector-spec").Warn(
			"openapi spec check: connector lookup failed", "traceID", toolCtx.TraceID, "connector", name, "error", err)
		return fmt.Sprintf("could not check whether connector %q already has a spec uploaded on the Connectors page (%v); %s",
			name, err, missingOpenAPISpecMessage)
	}
	switch {
	case existing.Exists && existing.HasResource:
		return ""
	case specURL != "":
		return foreignUploadedSpecMessage
	case existing.Exists:
		return existingWithoutUploadMessage
	default:
		return missingOpenAPISpecMessage
	}
}

func LookupConnectorByName(ctx context.Context, querier tool.ControlQuerier, name string) (ConnectorLookup, error) {
	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return ConnectorLookup{}, fmt.Errorf("encode connector lookup: %w", err)
	}
	raw, err := querier.Custom(ctx, "platform", "post", ConnectorLookupPath, body)
	if err != nil {
		return ConnectorLookup{}, err
	}
	var lookup ConnectorLookup
	if err := json.Unmarshal(raw, &lookup); err != nil {
		return ConnectorLookup{}, fmt.Errorf("parse connector lookup: %w", err)
	}
	return lookup, nil
}

func validateOpenAPIConnectorSpecs(ctx context.Context, components []map[string]any, toolCtx *tool.Context) []schema.ValidationError {
	if toolCtx == nil || toolCtx.ArtifactService == nil {
		return nil
	}
	facade := toolCtx.Facade()
	var errs []schema.ValidationError
	for i, c := range components {
		if ctx.Err() != nil {
			break
		}
		typ, _ := c["type"].(string)
		origin, _ := c["origin"].(string)
		name, _ := c["name"].(string)
		filename, _ := c["filename"].(string)
		if typ != "connector" || origin == "platform" || filename == "" || isBlockedComponent(c) {
			continue
		}
		data, _, err := facade.LoadArtifact(ctx, filename, nil)
		if err != nil || len(data) == 0 {
			continue
		}
		componentPath := fmt.Sprintf("components[%d]", i)
		var cfg map[string]any
		if err := json.Unmarshal(data, &cfg); err != nil {
			errs = append(errs, schema.ValidationError{
				Path:    componentPath,
				Message: fmt.Sprintf("connector '%s': config %s is not a JSON object: %v", name, filename, err),
			})
			continue
		}
		if msg := openAPISpecProblem(ctx, toolCtx, cfg); msg != "" {
			errs = append(errs, schema.ValidationError{
				Path:    componentPath + "." + specProblemField(cfg),
				Message: fmt.Sprintf("connector '%s': %s", name, msg),
			})
		}
	}
	return errs
}

func specProblemField(cfg map[string]any) string {
	values, _ := cfg["values"].(map[string]any)
	specURL, _ := values["specification_url"].(string)
	if _, ok := values["spec_url"]; ok && specURL == "" {
		return "values.spec_url"
	}
	return "values.specification_url"
}
