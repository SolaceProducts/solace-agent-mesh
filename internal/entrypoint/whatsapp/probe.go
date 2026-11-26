// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const probeTimeout = 5 * time.Second

const probeBodyCap = 64 << 10

const probeField = "display_phone_number"

type ProbeErrorClass string

const (
	ProbeClassIncomplete         ProbeErrorClass = "config_incomplete"
	ProbeClassTokenInvalid       ProbeErrorClass = "auth_credentials_invalid"
	ProbeClassTokenExpired       ProbeErrorClass = "auth_credentials_expired"
	ProbeClassPermissionDenied   ProbeErrorClass = "auth_permission_denied"
	ProbeClassPhoneNumberUnknown ProbeErrorClass = "phone_number_id_unknown"
	ProbeClassRateLimited        ProbeErrorClass = "rate_limited"
	ProbeClassNetworkUnreachable ProbeErrorClass = "network_unreachable"
	ProbeClassUnknown            ProbeErrorClass = "unknown"
)

type ProbeResult struct {
	Success          bool
	ErrorClass       ProbeErrorClass
	Hint             string
	MetaErrorCode    int
	MetaErrorSubcode int
}

type ProbeOptions struct {
	BaseURL    string
	APIVersion string
	Timeout    time.Duration
}

func (o ProbeOptions) baseURL() string {
	if o.BaseURL == "" {
		return defaultGraphAPIBaseURL
	}
	return strings.TrimRight(o.BaseURL, "/")
}

func (o ProbeOptions) apiVersion() string {
	if o.APIVersion == "" {
		return defaultGraphAPIVersion
	}
	return o.APIVersion
}

func (o ProbeOptions) timeout() time.Duration {
	if o.Timeout <= 0 {
		return probeTimeout
	}
	return o.Timeout
}

func newProbeClientOnSharedDefaultPool(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

func ProbeCredentials(ctx context.Context, accessToken, phoneNumberID string, opts ProbeOptions) ProbeResult {
	accessToken = strings.TrimSpace(accessToken)
	phoneNumberID = strings.TrimSpace(phoneNumberID)
	if accessToken == "" || phoneNumberID == "" {
		return ProbeResult{
			ErrorClass: ProbeClassIncomplete,
			Hint:       "Phone Number ID and Access Token are both required to test the connection.",
		}
	}

	ctx, cancel := context.WithTimeout(ctx, opts.timeout())
	defer cancel()

	target := fmt.Sprintf("%s/%s/%s?fields=%s", opts.baseURL(), opts.apiVersion(), url.PathEscape(phoneNumberID), probeField)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return ProbeResult{
			ErrorClass: ProbeClassUnknown,
			Hint:       "Could not build the Meta Graph API request. Check the Phone Number ID for stray characters.",
		}
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := newProbeClientOnSharedDefaultPool(opts.timeout()).Do(req)
	if err != nil {
		return probeTransportFailure(err)
	}
	defer resp.Body.Close() //nolint:errcheck // response body, read-only

	body, _ := io.ReadAll(io.LimitReader(resp.Body, probeBodyCap))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return probeSuccessOrWrongNode(body)
	}

	ge := parseGraphError(body)
	class, hint := classifyProbeFailure(resp.StatusCode, ge)
	return ProbeResult{
		ErrorClass:       class,
		Hint:             hint,
		MetaErrorCode:    ge.Code,
		MetaErrorSubcode: ge.Subcode,
	}
}

func probeSuccessOrWrongNode(body []byte) ProbeResult {
	var node struct {
		DisplayPhoneNumber string `json:"display_phone_number"`
	}
	if err := json.Unmarshal(body, &node); err != nil {
		return ProbeResult{
			ErrorClass: ProbeClassUnknown,
			Hint:       "The Graph API answered, but not with the JSON Meta returns — an egress proxy or captive portal is most likely answering instead of Meta. Check outbound network access before changing any credential.",
		}
	}
	if node.DisplayPhoneNumber == "" {
		return ProbeResult{
			ErrorClass: ProbeClassPhoneNumberUnknown,
			Hint:       "That ID exists but is not a WhatsApp phone number — it is most likely the WhatsApp Business Account ID, which sits next to it in WhatsApp Manager. Copy the Phone Number ID from the phone number's own row.",
		}
	}
	return ProbeResult{Success: true}
}

func probeTransportFailure(err error) ProbeResult {
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return ProbeResult{
			ErrorClass: ProbeClassNetworkUnreachable,
			Hint:       "Timed out reaching the Meta Graph API. Check that this deployment has outbound internet access and that no egress proxy is holding the connection.",
		}
	}
	return ProbeResult{
		ErrorClass: ProbeClassNetworkUnreachable,
		Hint:       "Could not reach the Meta Graph API. Check outbound network access, DNS, and any egress proxy or firewall rules.",
	}
}

func classifyProbeFailure(httpStatus int, ge graphError) (ProbeErrorClass, string) {
	switch {
	case ge.Code == codeOAuthException && ge.Subcode == subcodeTokenExpired:
		return ProbeClassTokenExpired,
			"The access token has expired. Generate a new permanent System User token in Meta Business Settings and paste it here."
	case ge.Code == codeOAuthException && ge.Subcode == subcodeTokenInvalid:
		return ProbeClassTokenInvalid,
			"Meta has invalidated the access token — it was revoked, or the System User's password changed. Generate a new permanent token in Meta Business Settings and paste it here."
	case ge.Code == codeOAuthException, ge.Code == codeAPISession:
		return ProbeClassTokenInvalid,
			"Meta rejected the access token. Generate a new permanent System User token in Meta Business Settings and paste it here."
	case ge.Code >= permissionCodeMin && ge.Code <= permissionCodeMax, ge.Code == codeAPIPermissionDenied:
		return ProbeClassPermissionDenied,
			"Meta accepted the token but will not let it read this phone number. Grant the System User the whatsapp_business_management and whatsapp_business_messaging permissions on this WhatsApp Business Account."
	case ge.Code == codeUnknownObject:
		return ProbeClassPhoneNumberUnknown,
			"Meta does not recognize this Phone Number ID for this token. Copy the Phone Number ID (digits only, not the phone number) from WhatsApp Manager, and check the token belongs to the same WhatsApp Business Account."
	}

	switch classifyGraphFailure(httpStatus, ge.Code) {
	case classCredentials:
		return ProbeClassTokenInvalid,
			"Meta rejected the credentials for this request. Check the Access Token and the System User's WhatsApp permissions in Meta Business Settings."
	case classRateLimited:
		return ProbeClassRateLimited,
			"Meta is rate limiting this app right now, so the credentials could not be verified. Wait a few minutes and test again."
	case classTransient:
		return ProbeClassNetworkUnreachable,
			"The Meta Graph API returned a server error, so the credentials could not be verified. Try again shortly."
	}
	return ProbeClassUnknown, fmt.Sprintf(
		"The request was rejected (HTTP %d, Meta error %d). Check the Phone Number ID and Access Token in WhatsApp Manager, and whether an egress proxy or firewall is answering instead of Meta.",
		httpStatus, ge.Code)
}
