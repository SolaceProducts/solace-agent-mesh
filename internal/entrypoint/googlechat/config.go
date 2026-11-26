// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"cmp"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	DeliveryPubSub = "pubsub"
	DeliveryHTTPS  = "https"
)

const emailClaimKey = "email"

const (
	defaultChatAPIBaseURL      = "https://chat.googleapis.com/"
	defaultPubSubAPIBaseURL    = "https://pubsub.googleapis.com/"
	defaultInitialStatusText   = "⏳ Working on it…"
	pubsubSubscriptionPrefix   = "projects/"
	pubsubSubscriptionInfix    = "/subscriptions/"
	configErrorPrefix          = "google_chat adapter: "
	allowedEmailDomainsKey     = "allowed_email_domains"
	testSkipGoogleAuthKey      = "test_skip_google_auth"
	minEditIntervalKey         = "min_edit_interval_ms"
	useUserIdentityInChannels  = "use_user_identity_in_channels"
	feedbackEnabledKey         = "feedback_enabled"
	identityClaimKeyConfigKey  = "identity_claim_key"
	initialStatusMessageKey    = "initial_status_message"
	serviceAccountKeyConfigKey = "service_account_key"
	threadHistoryLimitKey      = "thread_history_limit"
	artifactDriveFolderKey     = "artifact_drive_folder_id"
	artifactDriveAccessKey     = "artifact_drive_access_days"
	testDriveMaxBytesKey       = "test_drive_max_artifact_bytes"
	testDriveBackoffKey        = "test_drive_remove_backoff_ms"
)

const (
	DefaultArtifactDriveAccessDays = 30
	MaxArtifactDriveAccessDays     = 365
)

var driveFolderIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{10,128}$`)

func ValidDriveFolderID(id string) bool { return driveFolderIDRE.MatchString(id) }

const MaxMinEditIntervalMillis = 60_000

type Config struct {
	Delivery                 string
	PubSubSubscription       string
	ProjectNumber            string
	AudienceType             string
	EndpointURL              string
	SignerCertsURL           string
	AddOnServiceAccountEmail string

	ServiceAccountKey string

	DefaultAgentName string

	IdentityClaimKey    string
	AllowedEmailDomains []string

	RunAs                     string
	UseUserIdentityInChannels bool

	FeedbackEnabled bool

	MinEditInterval time.Duration

	InitialStatusMessage string

	ThreadHistoryLimit int

	ArtifactDriveFolderID   string
	ArtifactDriveAccessDays int
	DriveMaxArtifactBytes   int64
	DriveRemoveBackoff      time.Duration

	ChatAPIBaseURL     string
	PubSubAPIBaseURL   string
	DriveAPIBaseURL    string
	TestSkipGoogleAuth bool
}

func ParseConfig(raw map[string]any) (*Config, error) {
	cfg := &Config{
		IdentityClaimKey:     emailClaimKey,
		MinEditInterval:      perSpaceWriteInterval,
		InitialStatusMessage: defaultInitialStatusText,
		ChatAPIBaseURL:       defaultChatAPIBaseURL,
		PubSubAPIBaseURL:     defaultPubSubAPIBaseURL,
		DriveAPIBaseURL:      defaultDriveAPIBaseURL,

		ArtifactDriveAccessDays: DefaultArtifactDriveAccessDays,
		DriveMaxArtifactBytes:   defaultMaxDriveArtifactBytes,
		DriveRemoveBackoff:      driveRemoveBackoffDefault,
	}

	if _, err := optionalString(raw, "delivery"); err != nil {
		return nil, err
	}
	cfg.Delivery = ResolveDelivery(raw)
	switch cfg.Delivery {
	case DeliveryPubSub, DeliveryHTTPS:
	default:
		return nil, configError("delivery must be %q or %q, got %q", DeliveryPubSub, DeliveryHTTPS, cfg.Delivery)
	}

	var err error
	if cfg.PubSubSubscription, err = optionalString(raw, "pubsub_subscription"); err != nil {
		return nil, err
	}
	if cfg.Delivery == DeliveryPubSub {
		if subErr := validateSubscriptionName(cfg.PubSubSubscription); subErr != nil {
			return nil, subErr
		}
	}
	if audienceErr := parseHTTPSAudience(cfg, raw); audienceErr != nil {
		return nil, audienceErr
	}
	if cfg.ServiceAccountKey, err = optionalString(raw, serviceAccountKeyConfigKey); err != nil {
		return nil, err
	}
	if cfg.ServiceAccountKey != "" {
		if problem := ServiceAccountKeyProblem(cfg.ServiceAccountKey); problem != "" {
			return nil, configError("%s is not usable: %s", serviceAccountKeyConfigKey, problem)
		}
	}
	if cfg.DefaultAgentName, err = optionalString(raw, "default_agent_name"); err != nil {
		return nil, err
	}
	if cfg.RunAs, err = optionalString(raw, "run_as"); err != nil {
		return nil, err
	}

	claimKey, err := optionalString(raw, identityClaimKeyConfigKey)
	if err != nil {
		return nil, err
	}
	if raw[identityClaimKeyConfigKey] != nil && claimKey == "" {
		return nil, configError("%s must not be empty; omit it to match on %q", identityClaimKeyConfigKey, emailClaimKey)
	}
	if claimKey != "" {
		cfg.IdentityClaimKey = claimKey
	}

	if cfg.AllowedEmailDomains, err = parseDomainList(raw[allowedEmailDomainsKey]); err != nil {
		return nil, err
	}

	for key, dst := range map[string]*bool{
		useUserIdentityInChannels: &cfg.UseUserIdentityInChannels,
		feedbackEnabledKey:        &cfg.FeedbackEnabled,
		testSkipGoogleAuthKey:     &cfg.TestSkipGoogleAuth,
	} {
		if boolErr := optionalBool(raw, key, dst); boolErr != nil {
			return nil, boolErr
		}
	}

	if ms, set, intervalErr := optionalBoundedInt(raw, minEditIntervalKey, 0, MaxMinEditIntervalMillis); intervalErr != nil {
		return nil, intervalErr
	} else if set {
		cfg.MinEditInterval = time.Duration(ms) * time.Millisecond
	}

	if limit, set, limitErr := optionalBoundedInt(raw, threadHistoryLimitKey, 0, MaxThreadHistoryLimit); limitErr != nil {
		return nil, limitErr
	} else if set {
		cfg.ThreadHistoryLimit = limit
	}

	if driveErr := parseDriveSettings(cfg, raw); driveErr != nil {
		return nil, driveErr
	}

	if raw[initialStatusMessageKey] != nil {
		if cfg.InitialStatusMessage, err = optionalString(raw, initialStatusMessageKey); err != nil {
			return nil, err
		}
	}

	for key, dst := range map[string]*string{
		"api_base_url":    &cfg.ChatAPIBaseURL,
		"pubsub_base_url": &cfg.PubSubAPIBaseURL,
		"drive_base_url":  &cfg.DriveAPIBaseURL,
	} {
		override, err := optionalString(raw, key)
		if err != nil {
			return nil, err
		}
		if override != "" {
			*dst = strings.TrimRight(override, "/") + "/"
		}
	}
	if cfg.TestSkipGoogleAuth && (!isLoopbackURL(cfg.ChatAPIBaseURL) || !isLoopbackURL(cfg.PubSubAPIBaseURL) ||
		(cfg.ArtifactDriveFolderID != "" && !isLoopbackURL(cfg.DriveAPIBaseURL))) {
		return nil, configError("%s is a test-only key and requires api_base_url and pubsub_base_url, and drive_base_url when %s is set, on a loopback host", testSkipGoogleAuthKey, artifactDriveFolderKey)
	}
	if ms, set, backoffErr := optionalBoundedInt(raw, testDriveBackoffKey, 1, int(driveRemoveBackoffDefault/time.Millisecond)); backoffErr != nil {
		return nil, backoffErr
	} else if set {
		cfg.DriveRemoveBackoff = time.Duration(ms) * time.Millisecond
		if !cfg.TestSkipGoogleAuth {
			return nil, configError("%s is a test-only key and requires %s", testDriveBackoffKey, testSkipGoogleAuthKey)
		}
	}
	return cfg, nil
}

func parseDriveSettings(cfg *Config, raw map[string]any) error {
	var err error
	if cfg.ArtifactDriveFolderID, err = optionalString(raw, artifactDriveFolderKey); err != nil {
		return err
	}
	if cfg.ArtifactDriveFolderID != "" && !ValidDriveFolderID(cfg.ArtifactDriveFolderID) {
		return configError("%s must be the id of a Drive folder or shared drive, got %q", artifactDriveFolderKey, cfg.ArtifactDriveFolderID)
	}
	if days, set, daysErr := optionalBoundedInt(raw, artifactDriveAccessKey, 1, MaxArtifactDriveAccessDays); daysErr != nil {
		return daysErr
	} else if set {
		cfg.ArtifactDriveAccessDays = days
	}
	if limit, set, limitErr := optionalBoundedInt(raw, testDriveMaxBytesKey, 1, defaultMaxDriveArtifactBytes); limitErr != nil {
		return limitErr
	} else if set {
		cfg.DriveMaxArtifactBytes = int64(limit)
		if !cfg.TestSkipGoogleAuth {
			return configError("%s is a test-only key and requires %s", testDriveMaxBytesKey, testSkipGoogleAuthKey)
		}
	}
	return nil
}

func optionalBoundedInt(raw map[string]any, key string, lo, hi int) (value int, set bool, err error) {
	rawValue := raw[key]
	if rawValue == nil {
		return 0, false, nil
	}
	value, ok := parseInt(rawValue)
	if !ok || value < lo || value > hi {
		return 0, false, configError("%s must be an integer from %d to %d, got %v", key, lo, hi, rawValue)
	}
	return value, true, nil
}

func ResolveDelivery(raw map[string]any) string {
	if delivery, _ := raw["delivery"].(string); strings.TrimSpace(delivery) != "" {
		return strings.ToLower(strings.TrimSpace(delivery))
	}
	if legacySubscription, _ := raw["pubsub_subscription"].(string); strings.TrimSpace(legacySubscription) != "" {
		return DeliveryPubSub
	}
	return DeliveryHTTPS
}

type FieldProblem struct {
	Field   string
	Problem string
}

func (p FieldProblem) String() string { return p.Field + " " + p.Problem }

const AddOnSignerPattern = `^service-[0-9]+@gcp-sa-gsuiteaddons\.iam\.gserviceaccount\.com$`

var addOnSignerRE = regexp.MustCompile(AddOnSignerPattern)

func HTTPSAudienceProblems(raw map[string]any) []FieldProblem {
	if ResolveDelivery(raw) != DeliveryHTTPS {
		return nil
	}
	cfg := &Config{Delivery: DeliveryHTTPS}
	problems := readHTTPSAuth(cfg, raw)
	return append(problems, audienceProblems(cfg)...)
}

func parseHTTPSAudience(cfg *Config, raw map[string]any) error {
	if problems := readHTTPSAuth(cfg, raw); len(problems) > 0 {
		return configError("%s", problems[0])
	}
	if cfg.Delivery != DeliveryHTTPS {
		return nil
	}
	if problems := audienceProblems(cfg); len(problems) > 0 {
		return configError("%s", problems[0])
	}
	return nil
}

func readHTTPSAuth(cfg *Config, raw map[string]any) []FieldProblem {
	var problems []FieldProblem
	var audience string
	for _, setting := range []struct {
		field string
		dst   *string
	}{
		{"auth_audience_type", &audience},
		{"endpoint_url", &cfg.EndpointURL},
		{"signer_certs_url", &cfg.SignerCertsURL},
		{"addon_service_account_email", &cfg.AddOnServiceAccountEmail},
	} {
		value, err := optionalString(raw, setting.field)
		if err != nil {
			problems = append(problems, FieldProblem{Field: setting.field, Problem: "must be a string"})
		}
		*setting.dst = value
	}
	projectNumber, err := optionalScalar(raw, "project_number")
	if err != nil {
		problems = append(problems, FieldProblem{Field: "project_number", Problem: "must be a string or number"})
	}
	cfg.ProjectNumber = projectNumber
	cfg.AudienceType = cmp.Or(strings.ToLower(audience), AudienceEndpointURL)
	return problems
}

func audienceProblems(cfg *Config) []FieldProblem {
	var problems []FieldProblem
	if cfg.AddOnServiceAccountEmail != "" {
		if !addOnSignerRE.MatchString(cfg.AddOnServiceAccountEmail) {
			problems = append(problems, FieldProblem{Field: "addon_service_account_email", Problem: "must be the add-on's Google-managed account, service-<project number>@gcp-sa-gsuiteaddons.iam.gserviceaccount.com"})
		}
		if cfg.AudienceType != AudienceEndpointURL {
			problems = append(problems, FieldProblem{Field: "addon_service_account_email", Problem: fmt.Sprintf("requires auth_audience_type %q: add-on requests are addressed to the endpoint URL", AudienceEndpointURL)})
		}
	}
	switch cfg.AudienceType {
	case AudienceProjectNumber:
		if cfg.ProjectNumber == "" {
			problems = append(problems, FieldProblem{Field: "project_number", Problem: fmt.Sprintf("is required for delivery %q with auth_audience_type %q", DeliveryHTTPS, AudienceProjectNumber)})
		}
	case AudienceEndpointURL:
		if !isHTTPSURL(cfg.EndpointURL) {
			problems = append(problems, FieldProblem{Field: "endpoint_url", Problem: fmt.Sprintf("must be the app's https:// endpoint for auth_audience_type %q", AudienceEndpointURL)})
		}
	default:
		problems = append(problems, FieldProblem{Field: "auth_audience_type", Problem: fmt.Sprintf("must be %q or %q, got %q", AudienceProjectNumber, AudienceEndpointURL, cfg.AudienceType)})
	}
	if cfg.SignerCertsURL != "" && !isLoopbackURL(cfg.SignerCertsURL) {
		problems = append(problems, FieldProblem{Field: "signer_certs_url", Problem: "is a test-only key and must point at a loopback host"})
	}
	return problems
}

func isHTTPSURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != ""
}

func validateSubscriptionName(name string) error {
	if name == "" {
		return configError("pubsub_subscription is required for delivery %q", DeliveryPubSub)
	}
	project, sub, ok := strings.Cut(strings.TrimPrefix(name, pubsubSubscriptionPrefix), pubsubSubscriptionInfix)
	if !strings.HasPrefix(name, pubsubSubscriptionPrefix) || !ok || project == "" || sub == "" || strings.Contains(sub, "/") {
		return configError("pubsub_subscription must be projects/{project}/subscriptions/{subscription}, got %q", name)
	}
	return nil
}

func (c *Config) emailDomainAllowed(email string) bool {
	if len(c.AllowedEmailDomains) == 0 {
		return true
	}
	_, domain, ok := strings.Cut(email, "@")
	if !ok {
		return false
	}
	domain = strings.ToLower(domain)
	for _, allowed := range c.AllowedEmailDomains {
		if domain == allowed {
			return true
		}
	}
	return false
}

func parseDomainList(raw any) ([]string, error) {
	var entries []string
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, nil
		}
		for entry := range strings.SplitSeq(v, ",") {
			entries = append(entries, entry)
		}
	case []any:
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, configError("%s entries must be strings, got %T", allowedEmailDomainsKey, item)
			}
			entries = append(entries, s)
		}
	case []string:
		entries = v
	default:
		return nil, configError("%s must be a list of domains, got %T", allowedEmailDomainsKey, raw)
	}
	var domains []string
	for _, entry := range entries {
		domain, problem := NormalizeDomain(entry)
		if problem != "" {
			return nil, configError("%s %s", allowedEmailDomainsKey, problem)
		}
		if domain == "" {
			continue
		}
		domains = append(domains, domain)
	}
	if len(entries) > 0 && len(domains) == 0 {
		return nil, configError("%s names no domain; list at least one, or omit it to allow every domain", allowedEmailDomainsKey)
	}
	return domains, nil
}

func isLoopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func optionalString(raw map[string]any, key string) (string, error) {
	v, present := raw[key]
	if !present || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", configError("%s must be a string, got %T", key, v)
	}
	return strings.TrimSpace(s), nil
}

func optionalScalar(raw map[string]any, key string) (string, error) {
	switch v := raw[key].(type) {
	case nil:
		return "", nil
	case string:
		return strings.TrimSpace(v), nil
	case int:
		return strconv.Itoa(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	default:
		return "", configError("%s must be a string or number, got %T", key, v)
	}
}

func optionalBool(raw map[string]any, key string, dst *bool) error {
	v, present := raw[key]
	if !present || v == nil {
		return nil
	}
	switch b := v.(type) {
	case bool:
		*dst = b
		return nil
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(b))
		if err != nil {
			return configError("%s must be true or false, got %q", key, b)
		}
		*dst = parsed
		return nil
	}
	return configError("%s must be true or false, got %T", key, v)
}

func parseInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), n == float64(int(n))
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(n))
		return parsed, err == nil
	}
	return 0, false
}

func configError(format string, args ...any) error {
	return fmt.Errorf(configErrorPrefix+format, args...)
}
