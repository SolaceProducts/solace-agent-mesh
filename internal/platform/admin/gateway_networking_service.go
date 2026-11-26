// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/googlechat"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

var gatewayTypesRequiringIngress = map[string]bool{
	"teams":                      true,
	"mcp":                        true,
	"whatsapp":                   true,
	"webhook":                    true,
	platpkg.AgentLinkGatewayType: true,
}

func requiresIngress(gw *store.Gateway, logger *slog.Logger) bool {
	if gw.Type != "google_chat" {
		return gatewayTypesRequiringIngress[gw.Type]
	}
	var vals map[string]any
	if gw.Values != nil {
		if err := json.Unmarshal([]byte(*gw.Values), &vals); err != nil {
			logger.Warn("failed to parse gateway values; not surfacing its networking", "gatewayId", gw.ID, "gatewayType", gw.Type, "error", err)
			return false
		}
	}
	return googleChatDelivery(vals) == googlechat.DeliveryHTTPS
}

const AgentLinkConnectPath = "/v1/connect"

var ErrGatewayDNSNotProvisioned = errors.New("entrypoint has no dns_id")

type GatewayNetworkingService struct {
	gatewayRepo *store.GatewayRepo
	domain      string
	agentMeshID string
	logger      *slog.Logger
}

func NewGatewayNetworkingService(gatewayRepo *store.GatewayRepo, domain, agentMeshID string, logger *slog.Logger) *GatewayNetworkingService {
	return &GatewayNetworkingService{
		gatewayRepo: gatewayRepo,
		domain:      domain,
		agentMeshID: agentMeshID,
		logger:      logger,
	}
}

func (s *GatewayNetworkingService) GetNetworking(ctx context.Context, gatewayID string) (*GatewayNetworkingResponse, error) {
	gw, err := s.gatewayRepo.GetByID(ctx, gatewayID)
	if err != nil {
		return nil, fmt.Errorf("get gateway: %w", err)
	}
	if gw == nil {
		return nil, &NotFoundError{Resource: "entrypoint", ID: gatewayID}
	}

	if !requiresIngress(gw, s.logger) {
		return nil, nil
	}

	routePath := ""
	if gw.Type == "webhook" && gw.Values != nil {
		var vals struct {
			RoutePath string `json:"routePath"`
		}
		if jsonErr := json.Unmarshal([]byte(*gw.Values), &vals); jsonErr != nil {
			s.logger.Warn("failed to parse gateway values; using default route path", "gatewayId", gatewayID, "error", jsonErr)
		} else {
			routePath = vals.RoutePath
		}
	}
	if routePath == "" && gw.Type == "webhook" {
		routePath = "/"
	}

	apiPaths := getAPIPaths(gw.Type, routePath)
	publicURL := derefStr(gw.PublicURL)
	resp, err := s.networkingResponse(gw, apiPaths, publicURL)
	if err != nil {
		return nil, err
	}
	if gw.Type == "google_chat" {
		if endpoint := googleChatEndpointURL(gw); endpoint != "" {
			resp.WebhookURL = endpoint
		}
	}
	return resp, nil
}

func (s *GatewayNetworkingService) networkingResponse(gw *store.Gateway, apiPaths []GatewayAPIPath, publicURL string) (*GatewayNetworkingResponse, error) {

	if s.domain == "" {
		pathURL := "/gw/" + gatewayWireID(gw)
		return &GatewayNetworkingResponse{
			URL:        pathURL,
			WebhookURL: buildWebhookURL(pathURL, apiPaths),
			APIPaths:   apiPaths,
			PublicURL:  publicURL,
		}, nil
	}

	if gw.DNSID == nil || *gw.DNSID == "" {
		return nil, fmt.Errorf("gateway %s: %w", gw.ID, ErrGatewayDNSNotProvisioned)
	}

	hostname := fmt.Sprintf("gw-%s", *gw.DNSID)
	if s.agentMeshID != "" {
		hostname = fmt.Sprintf("gw-%s-%s", *gw.DNSID, s.agentMeshID)
	}
	dnsName := fmt.Sprintf("%s.%s", hostname, s.domain)

	externalPort := 443
	backendPort := 8443

	url := fmt.Sprintf("https://%s", dnsName)
	if externalPort != 443 {
		url = fmt.Sprintf("https://%s:%d", dnsName, externalPort)
	}

	return &GatewayNetworkingResponse{
		DNSName:     dnsName,
		Port:        externalPort,
		BackendPort: backendPort,
		URL:         url,
		WebhookURL:  buildWebhookURL(url, apiPaths),
		APIPaths:    apiPaths,
		PublicURL:   publicURL,
	}, nil
}

func buildWebhookURL(baseURL string, apiPaths []GatewayAPIPath) string {
	if baseURL == "" || len(apiPaths) == 0 {
		return ""
	}
	return baseURL + apiPaths[0].Path
}

func getAPIPaths(gatewayType, routePath string) []GatewayAPIPath {
	switch gatewayType {
	case "teams":
		return []GatewayAPIPath{
			{Path: "/api/messages", Description: "Bot Framework webhook endpoint"},
			{Path: "/health", Description: "Health check endpoint"},
		}
	case "mcp":
		return []GatewayAPIPath{
			{Path: "", Description: "MCP streamable HTTP endpoint (entrypoint root)"},
		}
	case "whatsapp":
		return []GatewayAPIPath{
			{Path: "/webhook", Description: "WhatsApp Business Cloud API webhook endpoint (GET verify + POST messages)"},
		}
	case "webhook":
		return []GatewayAPIPath{
			{Path: routePath, Description: "Webhook ingress route"},
		}
	case "google_chat":
		return []GatewayAPIPath{
			{Path: "/events", Description: "Google Chat HTTPS event endpoint"},
		}
	case platpkg.AgentLinkGatewayType:
		return []GatewayAPIPath{
			{Path: AgentLinkConnectPath, Description: "Agent link WebSocket endpoint that link agents connect to"},
		}
	default:
		return nil
	}
}
