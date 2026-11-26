// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

const templateListCap = 200

const templateBodyCap = 1024

const contactShareButtonType = "REQUEST_CONTACT_INFO"

const templateStatusApproved = "APPROVED"

var templateVariablePattern = regexp.MustCompile(`\{\{[^{}]*\}\}`)

type TemplateBlocker string

const (
	TemplateBlockerNoContactButton TemplateBlocker = "no_contact_button"
	TemplateBlockerHasVariables    TemplateBlocker = "has_variables"
	TemplateBlockerNotApproved     TemplateBlocker = "not_approved"
)

type MessageTemplate struct {
	Name     string
	Language string
	Status   string
	Body     string
	Usable   bool
	Blockers []TemplateBlocker
}

type MessageTemplateListResult struct {
	Templates        []MessageTemplate
	ErrorClass       ProbeErrorClass
	Hint             string
	HasMore          bool
	MetaErrorCode    int
	MetaErrorSubcode int
}

type graphTemplateList struct {
	Data   []graphTemplate `json:"data"`
	Paging struct {
		Next string `json:"next"`
	} `json:"paging"`
}

type graphTemplate struct {
	Name       string `json:"name"`
	Language   string `json:"language"`
	Status     string `json:"status"`
	Components []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Buttons []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			URL  string `json:"url"`
		} `json:"buttons"`
	} `json:"components"`
}

func ListMessageTemplates(ctx context.Context, accessToken, wabaID string, opts ProbeOptions) MessageTemplateListResult {
	accessToken = strings.TrimSpace(accessToken)
	wabaID = strings.TrimSpace(wabaID)
	if accessToken == "" || wabaID == "" {
		return MessageTemplateListResult{
			ErrorClass: ProbeClassIncomplete,
			Hint:       "WhatsApp Business Account ID and Access Token are both required to look up templates.",
		}
	}

	ctx, cancel := context.WithTimeout(ctx, opts.timeout())
	defer cancel()

	target := fmt.Sprintf("%s/%s/%s/message_templates?limit=%d",
		opts.baseURL(), opts.apiVersion(), url.PathEscape(wabaID), templateListCap)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return MessageTemplateListResult{
			ErrorClass: ProbeClassUnknown,
			Hint:       "Could not build the Meta Graph API request. Check the WhatsApp Business Account ID for stray characters.",
		}
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := (&http.Client{Timeout: opts.timeout()}).Do(req)
	if err != nil {
		failed := probeTransportFailure(err)
		return MessageTemplateListResult{ErrorClass: failed.ErrorClass, Hint: failed.Hint}
	}
	defer resp.Body.Close() //nolint:errcheck // response body, read-only

	body, _ := io.ReadAll(io.LimitReader(resp.Body, probeBodyCap))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		ge := parseGraphError(body)
		class, hint := classifyProbeFailure(resp.StatusCode, ge)
		return MessageTemplateListResult{
			ErrorClass:       class,
			Hint:             hint,
			MetaErrorCode:    ge.Code,
			MetaErrorSubcode: ge.Subcode,
		}
	}

	var list graphTemplateList
	if err := json.Unmarshal(body, &list); err != nil {
		return MessageTemplateListResult{
			ErrorClass: ProbeClassUnknown,
			Hint:       "The Meta Graph API returned a response this could not read. Check whether something on the network is intercepting outbound requests.",
		}
	}

	return MessageTemplateListResult{
		Templates: templatesFromGraph(list.Data),
		HasMore:   list.Paging.Next != "",
	}
}

func templatesFromGraph(in []graphTemplate) []MessageTemplate {
	out := make([]MessageTemplate, 0, len(in))
	for _, t := range in {
		out = append(out, messageTemplateFrom(t))
	}
	slices.SortStableFunc(out, func(a, b MessageTemplate) int {
		if a.Usable != b.Usable {
			if a.Usable {
				return -1
			}
			return 1
		}
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.Language, b.Language)
	})
	return out
}

func messageTemplateFrom(t graphTemplate) MessageTemplate {
	row := MessageTemplate{
		Name:     t.Name,
		Language: t.Language,
		Status:   t.Status,
		Body:     truncateRunes(templateBodyText(t), templateBodyCap),
	}
	if !hasContactShareButton(t) {
		row.Blockers = append(row.Blockers, TemplateBlockerNoContactButton)
	}
	if hasTemplateVariables(t) {
		row.Blockers = append(row.Blockers, TemplateBlockerHasVariables)
	}
	if !strings.EqualFold(strings.TrimSpace(t.Status), templateStatusApproved) {
		row.Blockers = append(row.Blockers, TemplateBlockerNotApproved)
	}
	row.Usable = len(row.Blockers) == 0
	return row
}

func templateBodyText(t graphTemplate) string {
	for _, c := range t.Components {
		if strings.EqualFold(c.Type, "BODY") && strings.TrimSpace(c.Text) != "" {
			return c.Text
		}
	}
	for _, c := range t.Components {
		if strings.TrimSpace(c.Text) != "" {
			return c.Text
		}
	}
	return ""
}

func hasContactShareButton(t graphTemplate) bool {
	for _, c := range t.Components {
		for _, b := range c.Buttons {
			if strings.EqualFold(strings.TrimSpace(b.Type), contactShareButtonType) {
				return true
			}
		}
	}
	return false
}

func hasTemplateVariables(t graphTemplate) bool {
	for _, c := range t.Components {
		if templateVariablePattern.MatchString(c.Text) {
			return true
		}
		for _, b := range c.Buttons {
			if templateVariablePattern.MatchString(b.Text) || templateVariablePattern.MatchString(b.URL) {
				return true
			}
		}
	}
	return false
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}
