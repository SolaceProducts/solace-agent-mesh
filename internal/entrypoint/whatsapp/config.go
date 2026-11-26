// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

const defaultGraphAPIVersion = "v26.0"

const defaultGraphAPIBaseURL = "https://graph.facebook.com"

const (
	IdentityLookupPhone      = "phone"
	IdentityLookupWAUsername = "wa_username"
)

const defaultIdentityLookupKey = IdentityLookupPhone

const defaultPhoneClaimKey = "phone_number"

const defaultTemplateLanguage = "en_US"

const defaultMaxMediaSizeMB = 25

type WhatsAppConfig struct {
	AccessToken   string
	PhoneNumberID string
	AppSecret     string
	VerifyToken   string

	DefaultAgentName string

	GraphAPIVersion string
	GraphAPIBaseURL string

	MaxMediaSizeMB int

	DisableFileUploads bool

	FeedbackEnabled bool

	DisableRateLimiting bool

	IdentityLookupKey string

	IdentityClaimKey string

	ContactRequestTemplate string

	ContactRequestTemplateLanguage string

	RunAs string
}

func ParseWhatsAppConfig(cfg map[string]any) (*WhatsAppConfig, error) {
	c := &WhatsAppConfig{
		GraphAPIVersion: defaultGraphAPIVersion,
		GraphAPIBaseURL: defaultGraphAPIBaseURL,
		MaxMediaSizeMB:  defaultMaxMediaSizeMB,
	}

	c.AccessToken = strings.TrimSpace(getString(cfg, "access_token"))
	if c.AccessToken == "" {
		return nil, fmt.Errorf("whatsapp adapter: access_token is required")
	}

	c.PhoneNumberID = strings.TrimSpace(getString(cfg, "phone_number_id"))
	if c.PhoneNumberID == "" {
		return nil, fmt.Errorf("whatsapp adapter: phone_number_id is required")
	}

	c.AppSecret = strings.TrimSpace(getString(cfg, "app_secret"))
	if c.AppSecret == "" {
		return nil, fmt.Errorf("whatsapp adapter: app_secret is required (webhook signature validation cannot be disabled)")
	}

	c.VerifyToken = strings.TrimSpace(getString(cfg, "verify_token"))
	if c.VerifyToken == "" {
		return nil, fmt.Errorf("whatsapp adapter: verify_token is required")
	}

	if v := strings.TrimSpace(getString(cfg, "default_agent_name")); v != "" {
		c.DefaultAgentName = v
	}
	if v := strings.TrimSpace(getString(cfg, "graph_api_version")); v != "" {
		c.GraphAPIVersion = v
	}
	if v := strings.TrimSpace(getString(cfg, "graph_api_base_url")); v != "" {
		c.GraphAPIBaseURL = strings.TrimRight(v, "/")
	}
	if raw, present := cfg["max_media_size_mb"]; present {
		v := getInt(cfg, "max_media_size_mb")
		if v <= 0 {
			return nil, fmt.Errorf("whatsapp adapter: max_media_size_mb must be a positive integer, got %v", raw)
		}
		c.MaxMediaSizeMB = v
	}

	if raw, present := cfg["disable_file_uploads"]; present {
		v, ok := parseBool(raw)
		if !ok {
			return nil, fmt.Errorf("whatsapp adapter: disable_file_uploads must be true or false, got %v", raw)
		}
		c.DisableFileUploads = v
	}

	if v, ok := cfg["disable_rate_limiting"].(bool); ok {
		c.DisableRateLimiting = v
	}

	if raw, present := cfg["feedback_enabled"]; present {
		v, ok := parseBool(raw)
		if !ok {
			return nil, fmt.Errorf("whatsapp adapter: feedback_enabled must be true or false, got %v", raw)
		}
		c.FeedbackEnabled = v
	}

	if raw, present := cfg["run_as"]; present {
		v, ok := parseConfigString(raw)
		if !ok {
			return nil, fmt.Errorf("whatsapp adapter: run_as must be a service account name string, got %v (%T) — quote the value (run_as: \"ops\"), which is also how a variable default stays a string rather than null (run_as: \"${VAR, }\")", raw, raw)
		}
		c.RunAs = strings.TrimSpace(v)
	}

	if err := parseIdentityLookup(cfg, c); err != nil {
		return nil, err
	}

	return c, nil
}

func parseIdentityLookup(cfg map[string]any, c *WhatsAppConfig) error {
	lookupKey, err := identityConfigString(cfg, "identity_lookup_key")
	if err != nil {
		return err
	}
	claimKey, err := identityConfigString(cfg, "identity_claim_key")
	if err != nil {
		return err
	}
	contactTemplate, err := identityConfigString(cfg, "contact_request_template")
	if err != nil {
		return err
	}
	contactLanguage, err := identityConfigString(cfg, "contact_request_template_language")
	if err != nil {
		return err
	}

	if c.RunAs != "" {
		if lookupKey != "" || claimKey != "" {
			return fmt.Errorf("whatsapp adapter: identity_lookup_key/identity_claim_key cannot be combined with run_as — a run_as entrypoint authorizes as its service account and never resolves the sender, so remove whichever of the two you did not mean")
		}
		if contactTemplate != "" || contactLanguage != "" {
			return fmt.Errorf("whatsapp adapter: contact_request_template cannot be combined with run_as — a run_as entrypoint never resolves the sender, so there is no identity a shared phone number would resolve")
		}
		return nil
	}

	c.IdentityLookupKey = cmp.Or(lookupKey, defaultIdentityLookupKey)
	switch c.IdentityLookupKey {
	case IdentityLookupPhone:
		c.IdentityClaimKey = cmp.Or(claimKey, defaultPhoneClaimKey)
		if !identity.SameClaimKey(c.IdentityClaimKey, defaultPhoneClaimKey) {
			return fmt.Errorf("whatsapp adapter: identity_lookup_key %q requires identity_claim_key %q, got %q — an inbound wa_id is compared digit-for-digit against %[2]q and as plain text against any other claim, so the two would never match; map the attribute onto %[2]q in your identity provider instead", c.IdentityLookupKey, defaultPhoneClaimKey, c.IdentityClaimKey)
		}
	case IdentityLookupWAUsername:
		if claimKey == "" {
			return fmt.Errorf("whatsapp adapter: identity_lookup_key %q requires identity_claim_key naming the identity-provider claim that holds each user's WhatsApp handle — there is no standard claim for it", c.IdentityLookupKey)
		}
		if identity.SameClaimKey(claimKey, defaultPhoneClaimKey) {
			return fmt.Errorf("whatsapp adapter: identity_lookup_key %q cannot be paired with identity_claim_key %q — a WhatsApp handle would be canonicalized as a phone number, so a handle spelled from somebody else's digits would match their claim; name the claim that holds each user's WhatsApp handle instead", c.IdentityLookupKey, defaultPhoneClaimKey)
		}
		c.IdentityClaimKey = claimKey
	default:
		return fmt.Errorf("whatsapp adapter: identity_lookup_key must be %q or %q, got %q", IdentityLookupPhone, IdentityLookupWAUsername, c.IdentityLookupKey)
	}
	return parseContactRequest(c, contactTemplate, contactLanguage)
}

func parseContactRequest(c *WhatsAppConfig, template, language string) error {
	if template == "" {
		if language != "" {
			return fmt.Errorf("whatsapp adapter: contact_request_template_language is set without contact_request_template, so no template would be sent — name the approved template, or remove the language")
		}
		return nil
	}
	if c.IdentityLookupKey != IdentityLookupPhone {
		return fmt.Errorf("whatsapp adapter: contact_request_template requires identity_lookup_key %q — a shared phone number cannot answer a %q lookup", IdentityLookupPhone, c.IdentityLookupKey)
	}
	c.ContactRequestTemplate = template
	c.ContactRequestTemplateLanguage = cmp.Or(language, defaultTemplateLanguage)
	return nil
}

func identityConfigString(cfg map[string]any, key string) (string, error) {
	raw, present := cfg[key]
	if !present {
		return "", nil
	}
	v, ok := parseConfigString(raw)
	if !ok {
		return "", fmt.Errorf("whatsapp adapter: %s must be a string, got %v (%T) — quote the value (%s: \"phone\")", key, raw, raw, key)
	}
	return strings.TrimSpace(v), nil
}

func getString(m map[string]any, key string) string {
	return scalarString(m[key])
}

func scalarString(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	default:
		return ""
	}
}

func getInt(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0
		}
		return n
	default:
		return 0
	}
}

func parseConfigString(v any) (value string, ok bool) {
	s, isString := v.(string)
	return s, isString
}

func parseBool(v any) (value, ok bool) {
	switch v := v.(type) {
	case bool:
		return v, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		return parsed, err == nil
	}
	return false, false
}
