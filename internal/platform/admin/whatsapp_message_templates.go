// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/whatsapp"
)

var listWhatsAppMessageTemplates = whatsapp.ListMessageTemplates

type WhatsAppMessageTemplatesRequest struct {
	BusinessAccountID string `json:"businessAccountId" validate:"required"`
	Values            any    `json:"values,omitempty"`
	GatewayID         string `json:"gatewayId,omitempty" validate:"omitempty,uuid"`
}

type WhatsAppMessageTemplate struct {
	Name     string   `json:"name"`
	Language string   `json:"language"`
	Status   string   `json:"status"`
	Body     string   `json:"body"`
	Usable   bool     `json:"usable"`
	Blockers []string `json:"blockers"`
}

type WhatsAppMessageTemplatesResponse struct {
	PaginatedResponse[WhatsAppMessageTemplate]
	Message    string `json:"message,omitempty"`
	ErrorClass string `json:"errorClass,omitempty"`
	HasMore    bool   `json:"hasMore,omitempty"`
}

func (s *GatewayService) ListWhatsAppMessageTemplates(ctx context.Context, req WhatsAppMessageTemplatesRequest) (*WhatsAppMessageTemplatesResponse, error) {
	values, gatewayType, err := s.resolveProbeValues(ctx, req.GatewayID, "whatsapp", req.Values)
	if err != nil {
		return nil, onField(err, fieldValues)
	}
	if values == nil {
		return nil, &ValidationError{Message: MsgEntrypointOrValuesRequired, Field: fieldValues}
	}
	if gatewayType != "whatsapp" {
		return nil, &ValidationError{Message: "message templates are a WhatsApp entrypoint feature", Field: fieldEntrypointID}
	}
	if !gatewayTypeVisible(gatewayType, s.featureRegistry) {
		return nil, errGatewayTypeDisabled(gatewayType)
	}

	token := asScalarString(values, "access_token")
	if isPlaceholder(token) || isPlaceholder(req.BusinessAccountID) {
		return &WhatsAppMessageTemplatesResponse{
			PaginatedResponse: unpagedResponse([]WhatsAppMessageTemplate{}),
			Message:           "This entrypoint uses substituted values, which only resolve where the entrypoint runs, so its templates cannot be listed from here.",
			ErrorClass:        string(whatsapp.ProbeClassIncomplete),
		}, nil
	}

	res := listWhatsAppMessageTemplates(ctx, token, req.BusinessAccountID, whatsapp.ProbeOptions{})
	if res.ErrorClass != "" {
		s.logger.LogAttrs(ctx, slog.LevelWarn, "whatsapp message template lookup failed",
			slog.String("eventType", "whatsapp.template_lookup_failed"),
			slog.String("errorClass", string(res.ErrorClass)),
			slog.Int("metaErrorCode", res.MetaErrorCode),
			slog.Int("metaErrorSubcode", res.MetaErrorSubcode))
		return &WhatsAppMessageTemplatesResponse{
			PaginatedResponse: unpagedResponse([]WhatsAppMessageTemplate{}),
			Message:           res.Hint,
			ErrorClass:        string(res.ErrorClass),
		}, nil
	}

	rows := make([]WhatsAppMessageTemplate, 0, len(res.Templates))
	for _, t := range res.Templates {
		rows = append(rows, WhatsAppMessageTemplate{
			Name:     t.Name,
			Language: t.Language,
			Status:   t.Status,
			Body:     t.Body,
			Usable:   t.Usable,
			Blockers: blockerStrings(t.Blockers),
		})
	}
	return &WhatsAppMessageTemplatesResponse{
		PaginatedResponse: unpagedResponse(rows),
		HasMore:           res.HasMore,
	}, nil
}

func blockerStrings(in []whatsapp.TemplateBlocker) []string {
	out := make([]string, 0, len(in))
	for _, b := range in {
		out = append(out, string(b))
	}
	return out
}
