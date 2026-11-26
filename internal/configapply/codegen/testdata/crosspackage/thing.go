package crosspackage

import "github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"

type CreateThingRequest struct {
	Name           string                `json:"name"`
	Pricing        *apiv2.Pricing        `json:"pricing,omitempty"`
	ToolsetConfigs []apiv2.ToolsetConfig `json:"toolsetConfigs"`
}

type ThingResponse struct {
	Name           string                `json:"name"`
	Pricing        *apiv2.Pricing        `json:"pricing"`
	ToolsetConfigs []apiv2.ToolsetConfig `json:"toolsetConfigs"`
	CreatedTime    *apiv2.Instant        `json:"createdTime"`
}
