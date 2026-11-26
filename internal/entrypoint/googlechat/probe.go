// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	chat "google.golang.org/api/chat/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	pubsub "google.golang.org/api/pubsub/v1"
)

const probeTimeout = 10 * time.Second

const subscriptionConsumePermission = "pubsub.subscriptions.consume"

const serviceDisabledReason = "SERVICE_DISABLED"

type ProbeErrorClass string

const (
	ProbeClassIncomplete           ProbeErrorClass = "config_incomplete"
	ProbeClassKeyInvalid           ProbeErrorClass = "service_account_key_invalid"
	ProbeClassCredentialsRejected  ProbeErrorClass = "auth_credentials_invalid"
	ProbeClassChatAPIDisabled      ProbeErrorClass = "chat_api_disabled"
	ProbeClassChatAppNotConfigured ProbeErrorClass = "chat_app_not_configured"
	ProbeClassPermissionDenied     ProbeErrorClass = "auth_permission_denied"
	ProbeClassPubSubAPIDisabled    ProbeErrorClass = "pubsub_api_disabled"
	ProbeClassSubscriptionNotFound ProbeErrorClass = "subscription_not_found"
	ProbeClassSubscriptionDenied   ProbeErrorClass = "subscription_permission_denied"
	ProbeClassRateLimited          ProbeErrorClass = "rate_limited"
	ProbeClassNetworkUnreachable   ProbeErrorClass = "network_unreachable"
	ProbeClassUnknown              ProbeErrorClass = "unknown"
)

const (
	ProbeStageInbound  = "inbound"
	ProbeStageOutbound = "outbound"
)

type ProbeRequest struct {
	ServiceAccountKey  string
	Delivery           string
	PubSubSubscription string
}

type ProbeResult struct {
	Success      bool
	Note         string
	ErrorClass   ProbeErrorClass
	Hint         string
	Stage        string
	GoogleStatus int
}

type ProbeOptions struct {
	ChatBaseURL   string
	PubSubBaseURL string
	TokenURL      string
	Timeout       time.Duration
}

func (o ProbeOptions) allowedTokenURIs() []string {
	if o.TokenURL != "" {
		return []string{o.TokenURL}
	}
	return googleTokenURIs
}

func Probe(ctx context.Context, req ProbeRequest, opts ProbeOptions) ProbeResult {
	key := strings.TrimSpace(req.ServiceAccountKey)
	if key == "" {
		return ProbeResult{
			ErrorClass: ProbeClassIncomplete,
			Hint:       "No service account key is set, so the gateway uses its own Application Default Credentials, which cannot be tested from here.",
			Stage:      ProbeStageOutbound,
		}
	}
	pullsSubscription, known := deliveryPullsSubscription(req)
	if !known {
		return ProbeResult{
			ErrorClass: ProbeClassIncomplete,
			Hint:       "Delivery must be pubsub or https.",
			Stage:      ProbeStageInbound,
		}
	}
	subscription := strings.TrimSpace(req.PubSubSubscription)
	if pullsSubscription && validateSubscriptionName(subscription) != nil {
		return ProbeResult{
			ErrorClass: ProbeClassIncomplete,
			Hint:       "Pub/Sub subscription must be the full resource name, projects/{project}/subscriptions/{subscription}.",
			Stage:      ProbeStageInbound,
		}
	}
	if res, ok := checkKeyShape(key, opts.allowedTokenURIs()); !ok {
		return res
	}

	timeout := cmp.Or(opts.Timeout, probeTimeout)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ctx = context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: timeout, CheckRedirect: refuseRedirect})

	tokens, res, ok := fetchProbeToken(ctx, key)
	if !ok {
		return res
	}
	if res, ok := probeChat(ctx, tokens, opts.ChatBaseURL); !ok {
		return res
	}
	if !pullsSubscription {
		return ProbeResult{Success: true, Note: ProbeNoteEndpointUntested}
	}
	if res, ok := probeSubscription(ctx, tokens, opts.PubSubBaseURL, subscription); !ok {
		return res
	}
	return ProbeResult{Success: true}
}

const ProbeNoteEndpointUntested = "The service account can use the Google Chat API. Whether Google can reach this entrypoint's HTTPS endpoint is not tested from here; send the Chat app a message to check."

func deliveryPullsSubscription(req ProbeRequest) (pulls, known bool) {
	switch ResolveDelivery(map[string]any{"delivery": req.Delivery, "pubsub_subscription": req.PubSubSubscription}) {
	case DeliveryPubSub:
		return true, true
	case DeliveryHTTPS:
		return false, true
	default:
		return false, false
	}
}

func refuseRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func checkKeyShape(key string, allowedTokenURIs []string) (ProbeResult, bool) {
	if problem := serviceAccountKeyProblem(key, allowedTokenURIs); problem != "" {
		return ProbeResult{ErrorClass: ProbeClassKeyInvalid, Hint: problem, Stage: ProbeStageOutbound}, false
	}
	return ProbeResult{}, true
}

func fetchProbeToken(ctx context.Context, key string) (oauth2.TokenSource, ProbeResult, bool) {
	creds, err := google.CredentialsFromJSONWithType(ctx, []byte(key), google.ServiceAccount, chatBotScope, pubsubScope)
	if err != nil {
		return nil, ProbeResult{
			ErrorClass: ProbeClassKeyInvalid,
			Hint:       "Service account key could not be read. Download a fresh JSON key from the Google Cloud console.",
			Stage:      ProbeStageOutbound,
		}, false
	}
	token, err := creds.TokenSource.Token()
	if err == nil {
		return oauth2.StaticTokenSource(token), ProbeResult{}, true
	}
	retrieveErr, ok := errors.AsType[*oauth2.RetrieveError](err)
	if !ok || retrieveErr.Response == nil {
		return nil, unreachable(ProbeStageOutbound), false
	}
	res := ProbeResult{Stage: ProbeStageOutbound, GoogleStatus: retrieveErr.Response.StatusCode}
	switch res.GoogleStatus {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
		res.ErrorClass = ProbeClassCredentialsRejected
		res.Hint = "Google rejected the service account key. It may have been deleted or disabled, or its service account removed."
		return nil, res, false
	default:
		return nil, classifyCommon(&googleapi.Error{Code: res.GoogleStatus}, res), false
	}
}

func probeChat(ctx context.Context, tokens oauth2.TokenSource, baseURL string) (ProbeResult, bool) {
	svc, err := chat.NewService(ctx, probeClientOptions(tokens, baseURL)...)
	if err != nil {
		return ProbeResult{ErrorClass: ProbeClassUnknown, Hint: "Could not build the Google Chat API client.", Stage: ProbeStageOutbound}, false
	}
	_, err = svc.Spaces.List().PageSize(1).Context(ctx).Do()
	if err == nil {
		return ProbeResult{}, true
	}
	apiErr, ok := errors.AsType[*googleapi.Error](err)
	if !ok {
		return unreachable(ProbeStageOutbound), false
	}
	res := ProbeResult{Stage: ProbeStageOutbound, GoogleStatus: apiErr.Code}
	switch {
	case isServiceDisabled(apiErr):
		res.ErrorClass = ProbeClassChatAPIDisabled
		res.Hint = "The Google Chat API is not enabled in the service account's Google Cloud project."
	case apiErr.Code == http.StatusNotFound:
		res.ErrorClass = ProbeClassChatAppNotConfigured
		res.Hint = "No Chat app is configured in the service account's Google Cloud project. Configure it under Google Chat API > Configuration."
	case apiErr.Code == http.StatusUnauthorized:
		res.ErrorClass = ProbeClassCredentialsRejected
		res.Hint = "Google Chat rejected the service account's credentials."
	case apiErr.Code == http.StatusForbidden:
		res.ErrorClass = ProbeClassPermissionDenied
		res.Hint = "Google Chat refused the service account. Check that the key belongs to the project where the Chat app is configured."
	default:
		res = classifyCommon(apiErr, res)
	}
	return res, false
}

func probeSubscription(ctx context.Context, tokens oauth2.TokenSource, baseURL, subscription string) (ProbeResult, bool) {
	svc, err := pubsub.NewService(ctx, probeClientOptions(tokens, baseURL)...)
	if err != nil {
		return ProbeResult{ErrorClass: ProbeClassUnknown, Hint: "Could not build the Cloud Pub/Sub client.", Stage: ProbeStageInbound}, false
	}
	granted, err := svc.Projects.Subscriptions.TestIamPermissions(subscription, &pubsub.TestIamPermissionsRequest{
		Permissions: []string{subscriptionConsumePermission},
	}).Context(ctx).Do()
	if err == nil {
		if slices.Contains(granted.Permissions, subscriptionConsumePermission) {
			return ProbeResult{}, true
		}
		return subscriptionDenied(0), false
	}
	apiErr, ok := errors.AsType[*googleapi.Error](err)
	if !ok {
		return unreachable(ProbeStageInbound), false
	}
	switch {
	case isServiceDisabled(apiErr):
		return ProbeResult{
			ErrorClass:   ProbeClassPubSubAPIDisabled,
			Hint:         "The Cloud Pub/Sub API is not enabled in the service account's Google Cloud project.",
			Stage:        ProbeStageInbound,
			GoogleStatus: apiErr.Code,
		}, false
	case apiErr.Code == http.StatusNotFound:
		return ProbeResult{
			ErrorClass:   ProbeClassSubscriptionNotFound,
			Hint:         "The Pub/Sub subscription does not exist. Check the project and subscription names.",
			Stage:        ProbeStageInbound,
			GoogleStatus: apiErr.Code,
		}, false
	case apiErr.Code == http.StatusForbidden:
		return subscriptionDenied(apiErr.Code), false
	default:
		return classifyCommon(apiErr, ProbeResult{Stage: ProbeStageInbound, GoogleStatus: apiErr.Code}), false
	}
}

func subscriptionDenied(status int) ProbeResult {
	return ProbeResult{
		ErrorClass:   ProbeClassSubscriptionDenied,
		Hint:         "The service account may not pull the Pub/Sub subscription. Grant it the Pub/Sub Subscriber role on the subscription.",
		Stage:        ProbeStageInbound,
		GoogleStatus: status,
	}
}

func classifyCommon(apiErr *googleapi.Error, res ProbeResult) ProbeResult {
	if apiErr.Code == http.StatusTooManyRequests {
		res.ErrorClass = ProbeClassRateLimited
		res.Hint = "Google is rate limiting requests. Try again in a minute."
		return res
	}
	res.ErrorClass = ProbeClassUnknown
	res.Hint = "Google rejected the request for an unrecognized reason."
	return res
}

func unreachable(stage string) ProbeResult {
	return ProbeResult{
		ErrorClass: ProbeClassNetworkUnreachable,
		Hint:       "Could not reach Google. Check that this deployment allows outbound HTTPS to googleapis.com.",
		Stage:      stage,
	}
}

func probeClientOptions(tokens oauth2.TokenSource, baseURL string) []option.ClientOption {
	opts := []option.ClientOption{option.WithTokenSource(tokens)}
	if baseURL != "" {
		opts = append(opts, option.WithEndpoint(baseURL))
	}
	return opts
}

func isServiceDisabled(apiErr *googleapi.Error) bool {
	if apiErr.Code != http.StatusForbidden {
		return false
	}
	for _, detail := range apiErr.Details {
		fields, ok := detail.(map[string]any)
		if ok && fields["reason"] == serviceDisabledReason {
			return true
		}
	}
	return slices.ContainsFunc(apiErr.Errors, func(item googleapi.ErrorItem) bool {
		return item.Reason == "accessNotConfigured"
	})
}
