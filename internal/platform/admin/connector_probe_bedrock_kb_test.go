// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awsretry"
)

type stubBedrockKBClient struct {
	err        error
	recordedKB string
	calls      int
}

func (s *stubBedrockKBClient) GetKnowledgeBase(_ context.Context, in *bedrockagent.GetKnowledgeBaseInput, _ ...func(*bedrockagent.Options)) (*bedrockagent.GetKnowledgeBaseOutput, error) {
	s.calls++
	if in != nil && in.KnowledgeBaseId != nil {
		s.recordedKB = *in.KnowledgeBaseId
	}
	if s.err != nil {
		return nil, s.err
	}
	return &bedrockagent.GetKnowledgeBaseOutput{}, nil
}

func installStubBedrockKBClient(t *testing.T, stub *stubBedrockKBClient, recordedRegion *string, recordedAuth *bedrockKBProbeAuth) {
	t.Helper()
	prev := bedrockKBProbeClientBuilder
	bedrockKBProbeClientBuilder = func(_ context.Context, region string, auth bedrockKBProbeAuth) (bedrockKBProbeClient, error) {
		if recordedRegion != nil {
			*recordedRegion = region
		}
		if recordedAuth != nil {
			*recordedAuth = auth
		}
		return stub, nil
	}
	t.Cleanup(func() { bedrockKBProbeClientBuilder = prev })
}

func TestProbeBedrockKB_RegistryWiresKnowledgeBaseBedrock(t *testing.T) {
	if _, ok := connectorProbes[connectorProbeKey("knowledge_base", "bedrock")]; !ok {
		t.Fatal("knowledge_base:bedrock probe is not registered — Create/Update paths will skip AWS validation")
	}
}

func TestProbeBedrockKB_SuccessAccessKey(t *testing.T) {
	stub := &stubBedrockKBClient{}
	var gotRegion string
	var gotAuth bedrockKBProbeAuth
	installStubBedrockKBClient(t, stub, &gotRegion, &gotAuth)

	values := map[string]any{
		"kb_id":                 "ABC123XYZ",
		"region":                "us-west-2",
		"auth_type":             "access_key",
		"aws_access_key_id":     "AKIAIOSFODNN7EXAMPLE",
		"aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}
	if _, err := probeBedrockKB(t.Context(), discardLogger(), values, false); err != nil {
		t.Fatalf("probe returned %v on a success-path stub, want nil", err)
	}
	if stub.calls != 1 {
		t.Errorf("GetKnowledgeBase call count = %d, want 1", stub.calls)
	}
	if stub.recordedKB != "ABC123XYZ" {
		t.Errorf("kb_id forwarded to AWS = %q, want %q", stub.recordedKB, "ABC123XYZ")
	}
	if gotRegion != "us-west-2" {
		t.Errorf("region forwarded to client builder = %q, want %q", gotRegion, "us-west-2")
	}
	if gotAuth.accessKeyID != "AKIAIOSFODNN7EXAMPLE" {
		t.Errorf("accessKeyID forwarded = %q, want it preserved verbatim", gotAuth.accessKeyID)
	}
	if gotAuth.roleARN != "" {
		t.Errorf("roleARN forwarded = %q, want empty for access_key auth", gotAuth.roleARN)
	}
}

func TestProbeBedrockKB_AWSAuthFailureSurfacesRedactedMessage(t *testing.T) {
	stub := &stubBedrockKBClient{
		err: errors.New("InvalidSignatureException: signature did not match (request id abc-123)"),
	}
	installStubBedrockKBClient(t, stub, nil, nil)

	values := map[string]any{
		"kb_id":                 "BOGUSKB12345",
		"region":                "us-east-1",
		"auth_type":             "access_key",
		"aws_access_key_id":     "AKIAIOSFODNN7EXAMPLE",
		"aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}
	_, err := probeBedrockKB(t.Context(), discardLogger(), values, false)
	if err == nil {
		t.Fatal("probe returned nil on an AWS-error stub, want *ValidationError")
	}
	verr, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("probe returned %T (%v), want *ValidationError so handleServiceError maps to 422", err, err)
	}
	if verr.Message != bedrockKBProbeFailureMessage {
		t.Errorf("probe error message = %q, want the canonical popover copy %q (raw AWS error must not leak)",
			verr.Message, bedrockKBProbeFailureMessage)
	}
}

func TestProbeBedrockKB_ConnectivityFailureSurfacesUnreachableMessage(t *testing.T) {
	stub := &stubBedrockKBClient{err: context.DeadlineExceeded}
	installStubBedrockKBClient(t, stub, nil, nil)

	values := map[string]any{
		"kb_id":                 "BOGUSKB12345",
		"region":                "us-east-1",
		"auth_type":             "access_key",
		"aws_access_key_id":     "AKIAIOSFODNN7EXAMPLE",
		"aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}
	_, err := probeBedrockKB(t.Context(), discardLogger(), values, false)
	verr, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("probe returned %T (%v), want *ValidationError", err, err)
	}
	if verr.Message != bedrockKBProbeUnreachableMessage {
		t.Errorf("probe error message = %q, want the unreachable copy %q", verr.Message, bedrockKBProbeUnreachableMessage)
	}
}

func TestBedrockKBProbeUnreachable_Classifies(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"aws api error (reachable)", &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "denied"}, false},
		{"deadline exceeded", context.DeadlineExceeded, true},
		{"wrapped deadline", fmt.Errorf("operation error: %w", context.DeadlineExceeded), true},
		{"transport send failure", &smithyhttp.RequestSendError{Err: errors.New("dial tcp: i/o timeout")}, true},
		{"plain error defaults reachable", errors.New("load aws config: boom"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := bedrockKBProbeUnreachable(tc.err); got != tc.want {
				t.Errorf("bedrockKBProbeUnreachable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestProbeBedrockKB_ClientBuildFailureSurfacesRedactedMessage(t *testing.T) {
	prev := bedrockKBProbeClientBuilder
	bedrockKBProbeClientBuilder = func(_ context.Context, _ string, _ bedrockKBProbeAuth) (bedrockKBProbeClient, error) {
		return nil, errors.New("load aws config: explicit failure for test")
	}
	t.Cleanup(func() { bedrockKBProbeClientBuilder = prev })

	values := map[string]any{
		"kb_id":                 "ABC",
		"region":                "us-east-1",
		"auth_type":             "access_key",
		"aws_access_key_id":     "AKIAIOSFODNN7EXAMPLE",
		"aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}
	_, err := probeBedrockKB(t.Context(), discardLogger(), values, false)
	verr, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("probe returned %T (%v), want *ValidationError", err, err)
	}
	if verr.Message != bedrockKBProbeFailureMessage {
		t.Errorf("probe error message = %q, want the canonical popover copy %q",
			verr.Message, bedrockKBProbeFailureMessage)
	}
}

func TestProbeBedrockKB_SkipsPlaceholderSecrets(t *testing.T) {
	stub := &stubBedrockKBClient{}
	installStubBedrockKBClient(t, stub, nil, nil)

	values := map[string]any{
		"kb_id":                 "ABC123",
		"region":                "us-east-1",
		"auth_type":             "access_key",
		"aws_access_key_id":     "<<__SAM_REQUIRED__>>",
		"aws_secret_access_key": "<<__SAM_REQUIRED__>>",
	}
	if _, err := probeBedrockKB(t.Context(), discardLogger(), values, false); err != nil {
		t.Errorf("probe returned %v on placeholder credentials, want nil (skip)", err)
	}
	if stub.calls != 0 {
		t.Errorf("GetKnowledgeBase calls = %d, want 0 (probe must short-circuit before AWS)", stub.calls)
	}
}

func TestProbeBedrockKB_SkipsEmptyCredentials(t *testing.T) {
	stub := &stubBedrockKBClient{}
	installStubBedrockKBClient(t, stub, nil, nil)

	values := map[string]any{
		"kb_id":     "ABC123",
		"region":    "us-east-1",
		"auth_type": "access_key",
	}
	if _, err := probeBedrockKB(t.Context(), discardLogger(), values, false); err != nil {
		t.Errorf("probe returned %v on missing credentials, want nil (skip)", err)
	}
	if stub.calls != 0 {
		t.Errorf("GetKnowledgeBase calls = %d, want 0", stub.calls)
	}
}

func TestProbeBedrockKB_IAMSynthesizesRoleARN(t *testing.T) {
	stub := &stubBedrockKBClient{}
	var gotAuth bedrockKBProbeAuth
	installStubBedrockKBClient(t, stub, nil, &gotAuth)

	values := map[string]any{
		"kb_id":          "ABC123",
		"region":         "us-east-1",
		"auth_type":      "iam",
		"aws_account_id": "123456789012",
		"role_name":      "SolaceBedrockKBAccess",
	}
	if _, err := probeBedrockKB(t.Context(), discardLogger(), values, false); err != nil {
		t.Fatalf("probe returned %v on a success-path stub, want nil", err)
	}
	wantARN := "arn:aws:iam::123456789012:role/SolaceBedrockKBAccess"
	if gotAuth.roleARN != wantARN {
		t.Errorf("roleARN forwarded = %q, want %q", gotAuth.roleARN, wantARN)
	}
	if gotAuth.accessKeyID != "" || gotAuth.secretAccessKey != "" {
		t.Errorf("static creds = (%q, <secret>), want empty for iam auth", gotAuth.accessKeyID)
	}
}

func TestProbeBedrockKB_IAMSkipsWhenAccountOrRoleMissing(t *testing.T) {
	stub := &stubBedrockKBClient{}
	installStubBedrockKBClient(t, stub, nil, nil)

	cases := []map[string]any{
		{
			"kb_id": "ABC", "region": "us-east-1", "auth_type": "iam",
			"role_name": "OnlyRole",
		},
		{
			"kb_id": "ABC", "region": "us-east-1", "auth_type": "iam",
			"aws_account_id": "123456789012",
		},
	}
	for i, values := range cases {
		if _, err := probeBedrockKB(t.Context(), discardLogger(), values, false); err != nil {
			t.Errorf("case %d: probe returned %v, want nil (skip)", i, err)
		}
	}
	if stub.calls != 0 {
		t.Errorf("GetKnowledgeBase calls = %d, want 0 across all skip cases", stub.calls)
	}
}

func TestProbeBedrockKB_SkipsEmptyOrPlaceholderKBID(t *testing.T) {
	stub := &stubBedrockKBClient{}
	installStubBedrockKBClient(t, stub, nil, nil)

	cases := []map[string]any{
		{"kb_id": "", "auth_type": "access_key", "aws_access_key_id": "AKIA0000000000000001", "aws_secret_access_key": "1234567890123456789012345678901234567890"},
		{"kb_id": "<<__SAM_REQUIRED__>>", "auth_type": "access_key", "aws_access_key_id": "AKIA0000000000000001", "aws_secret_access_key": "1234567890123456789012345678901234567890"},
	}
	for i, values := range cases {
		if _, err := probeBedrockKB(t.Context(), discardLogger(), values, false); err != nil {
			t.Errorf("case %d: probe returned %v, want nil (skip)", i, err)
		}
	}
	if stub.calls != 0 {
		t.Errorf("GetKnowledgeBase calls = %d, want 0", stub.calls)
	}
}

func TestProbeBedrockKB_DefaultsRegionWhenAbsent(t *testing.T) {
	stub := &stubBedrockKBClient{}
	var gotRegion string
	installStubBedrockKBClient(t, stub, &gotRegion, nil)

	values := map[string]any{
		"kb_id":                 "ABC123XYZ",
		"auth_type":             "access_key",
		"aws_access_key_id":     "AKIAIOSFODNN7EXAMPLE",
		"aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}
	if _, err := probeBedrockKB(t.Context(), discardLogger(), values, false); err != nil {
		t.Fatalf("probe returned %v on a success-path stub, want nil", err)
	}
	if gotRegion != "us-east-1" {
		t.Errorf("region forwarded to client builder = %q, want default %q", gotRegion, "us-east-1")
	}
}

func TestBedrockKBProbeAuthFromValues_Branches(t *testing.T) {
	tests := []struct {
		name     string
		values   map[string]any
		wantSkip bool
		wantType string
		wantARN  string
	}{
		{
			name: "empty auth_type defaults to access_key",
			values: map[string]any{
				"aws_access_key_id":     "AKIAIOSFODNN7EXAMPLE",
				"aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			},
			wantSkip: false,
			wantType: "access_key",
		},
		{
			name:     "iam with placeholder account skips",
			values:   map[string]any{"auth_type": "iam", "aws_account_id": "<<__SAM_REQUIRED__>>", "role_name": "Role"},
			wantSkip: true,
			wantType: "iam",
		},
		{
			name:     "iam with placeholder role skips",
			values:   map[string]any{"auth_type": "iam", "aws_account_id": "123456789012", "role_name": "<<__SAM_REQUIRED__>>"},
			wantSkip: true,
			wantType: "iam",
		},
		{
			name:     "iam synthesizes role ARN",
			values:   map[string]any{"auth_type": "iam", "aws_account_id": "123456789012", "role_name": "SolaceBedrockKBAccess"},
			wantSkip: false,
			wantType: "iam",
			wantARN:  "arn:aws:iam::123456789012:role/SolaceBedrockKBAccess",
		},
		{
			name:     "unknown auth_type skips",
			values:   map[string]any{"auth_type": "totally_bogus"},
			wantSkip: true,
			wantType: "totally_bogus",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			auth, skip := bedrockKBProbeAuthFromValues(tc.values)
			if skip != tc.wantSkip {
				t.Errorf("skip = %v, want %v", skip, tc.wantSkip)
			}
			if auth.authType != tc.wantType {
				t.Errorf("authType = %q, want %q", auth.authType, tc.wantType)
			}
			if auth.roleARN != tc.wantARN {
				t.Errorf("roleARN = %q, want %q", auth.roleARN, tc.wantARN)
			}
		})
	}
}

func TestNewBedrockKBProbeClient_AccessKeyBuildsClient(t *testing.T) {
	auth := bedrockKBProbeAuth{
		authType:        "access_key",
		accessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		secretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}
	client, err := newBedrockKBProbeClient(t.Context(), "us-west-2", auth)
	if err != nil {
		t.Fatalf("newBedrockKBProbeClient: %v", err)
	}
	if client == nil {
		t.Fatal("newBedrockKBProbeClient returned nil client, want a usable bedrock-agent client")
	}
}

func TestLoadBedrockKBProbeAWSConfig_KeepsLegacyRetryer(t *testing.T) {
	auth := bedrockKBProbeAuth{
		authType:        "access_key",
		accessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		secretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}
	cfg, err := loadBedrockKBProbeAWSConfig(t.Context(), "us-west-2", auth)
	if err != nil {
		t.Fatalf("loadBedrockKBProbeAWSConfig: %v", err)
	}
	if cfg.Retryer == nil || !awsretry.IsLegacy(cfg.Retryer()) {
		t.Error("probe config does not carry the legacy retryer")
	}
	client := bedrockagent.NewFromConfig(cfg)
	if !awsretry.IsLegacy(client.Options().Retryer) {
		t.Error("bedrock-agent client does not use the legacy retryer")
	}
}

func TestLoadBedrockKBProbeAWSConfig_NoRoleReturnsStaticCreds(t *testing.T) {
	auth := bedrockKBProbeAuth{
		authType:        "access_key",
		accessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		secretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}
	cfg, err := loadBedrockKBProbeAWSConfig(t.Context(), "eu-central-1", auth)
	if err != nil {
		t.Fatalf("loadBedrockKBProbeAWSConfig: %v", err)
	}
	if cfg.Region != "eu-central-1" {
		t.Errorf("cfg.Region = %q, want eu-central-1", cfg.Region)
	}
	got, err := cfg.Credentials.Retrieve(t.Context())
	if err != nil {
		t.Fatalf("Credentials.Retrieve: %v", err)
	}
	if got.AccessKeyID != "AKIAIOSFODNN7EXAMPLE" {
		t.Errorf("resolved AccessKeyID = %q, want the static config key", got.AccessKeyID)
	}
}

func fakeSTSHandler(t *testing.T, capturedExternalID *string, failWith string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if capturedExternalID != nil {
			*capturedExternalID = r.FormValue("ExternalId")
		}
		w.Header().Set("Content-Type", "text/xml")
		if failWith != "" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`<ErrorResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">` +
				`<Error><Type>Sender</Type><Code>` + failWith + `</Code>` +
				`<Message>denied</Message></Error><RequestId>req-err</RequestId></ErrorResponse>`))
			return
		}
		_, _ = w.Write([]byte(`<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">` +
			`<AssumeRoleResult><Credentials>` +
			`<AccessKeyId>ASIAFAKEASSUMEDKEY01</AccessKeyId>` +
			`<SecretAccessKey>fakeAssumedSecretAccessKey0000000000000</SecretAccessKey>` +
			`<SessionToken>fake-session-token</SessionToken>` +
			`<Expiration>2999-01-01T00:00:00Z</Expiration>` +
			`</Credentials><AssumedRoleUser>` +
			`<Arn>arn:aws:sts::123456789012:assumed-role/Role/sess</Arn>` +
			`<AssumedRoleId>ARO123:sess</AssumedRoleId>` +
			`</AssumedRoleUser></AssumeRoleResult>` +
			`<ResponseMetadata><RequestId>req-ok</RequestId></ResponseMetadata></AssumeRoleResponse>`))
	}
}

func TestLoadBedrockKBProbeAWSConfig_UsesShortDialTimeoutClient(t *testing.T) {
	auth := bedrockKBProbeAuth{
		authType:        "access_key",
		accessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		secretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}
	cfg, err := loadBedrockKBProbeAWSConfig(t.Context(), "us-east-1", auth)
	if err != nil {
		t.Fatalf("loadBedrockKBProbeAWSConfig: %v", err)
	}
	hc, ok := cfg.HTTPClient.(*http.Client)
	if !ok {
		t.Fatalf("cfg.HTTPClient = %T, want *http.Client carrying the short-dial transport", cfg.HTTPClient)
	}
	tr, ok := hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("client transport = %T, want *http.Transport", hc.Transport)
	}
	if tr.TLSHandshakeTimeout != bedrockKBProbeDialTimeout {
		t.Errorf("TLSHandshakeTimeout = %v, want %v", tr.TLSHandshakeTimeout, bedrockKBProbeDialTimeout)
	}
}

func TestLoadBedrockKBProbeAWSConfig_AssumeRoleSuccess(t *testing.T) {
	var gotExternalID string
	srv := httptest.NewServer(fakeSTSHandler(t, &gotExternalID, ""))
	t.Cleanup(srv.Close)
	t.Setenv("AWS_ENDPOINT_URL_STS", srv.URL)

	auth := bedrockKBProbeAuth{
		authType:        "iam",
		accessKeyID:     "AKIABASEKEYEXAMPLE01",
		secretAccessKey: "baseSecretAccessKeyExample00000000000000",
		roleARN:         "arn:aws:iam::123456789012:role/SolaceBedrockKBAccess",
		externalID:      "trust-ext-id-42",
		sessionName:     "custom-session",
	}
	cfg, err := loadBedrockKBProbeAWSConfig(t.Context(), "us-east-1", auth)
	if err != nil {
		t.Fatalf("loadBedrockKBProbeAWSConfig: %v", err)
	}
	got, err := cfg.Credentials.Retrieve(t.Context())
	if err != nil {
		t.Fatalf("Credentials.Retrieve: %v", err)
	}
	if got.AccessKeyID != "ASIAFAKEASSUMEDKEY01" {
		t.Errorf("resolved AccessKeyID = %q, want the assumed-role temp key", got.AccessKeyID)
	}
	if got.SessionToken != "fake-session-token" {
		t.Errorf("resolved SessionToken = %q, want the assumed-role session token", got.SessionToken)
	}
	if gotExternalID != "trust-ext-id-42" {
		t.Errorf("ExternalId forwarded to STS = %q, want %q", gotExternalID, "trust-ext-id-42")
	}
}

func TestLoadBedrockKBProbeAWSConfig_AssumeRoleFailureWraps(t *testing.T) {
	srv := httptest.NewServer(fakeSTSHandler(t, nil, "AccessDenied"))
	t.Cleanup(srv.Close)
	t.Setenv("AWS_ENDPOINT_URL_STS", srv.URL)

	auth := bedrockKBProbeAuth{
		authType:        "iam",
		accessKeyID:     "AKIABASEKEYEXAMPLE01",
		secretAccessKey: "baseSecretAccessKeyExample00000000000000",
		roleARN:         "arn:aws:iam::123456789012:role/Denied",
	}
	_, err := loadBedrockKBProbeAWSConfig(t.Context(), "us-east-1", auth)
	if err == nil {
		t.Fatal("loadBedrockKBProbeAWSConfig returned nil, want an assume-role error")
	}
	if !strings.Contains(err.Error(), "assume role") {
		t.Errorf("error = %q, want it wrapped with %q context", err.Error(), "assume role")
	}
}

func TestRunConnectorProbe_DispatchesAndIgnoresUnknown(t *testing.T) {
	calls := 0
	stub := &stubBedrockKBClient{}
	prev := bedrockKBProbeClientBuilder
	bedrockKBProbeClientBuilder = func(_ context.Context, _ string, _ bedrockKBProbeAuth) (bedrockKBProbeClient, error) {
		calls++
		return stub, nil
	}
	t.Cleanup(func() { bedrockKBProbeClientBuilder = prev })

	values := map[string]any{
		"kb_id":                 "ABC",
		"region":                "us-east-1",
		"auth_type":             "access_key",
		"aws_access_key_id":     "AKIA0000000000000001",
		"aws_secret_access_key": "1234567890123456789012345678901234567890",
	}

	logger := discardLogger()
	if _, err := runConnectorProbe(t.Context(), logger, "knowledge_base", "bedrock", values, false); err != nil {
		t.Fatalf("dispatch to registered probe returned %v, want nil", err)
	}
	if calls != 1 {
		t.Errorf("registered probe invoked %d times, want 1", calls)
	}

	if _, err := runConnectorProbe(t.Context(), logger, "slack", "", values, false); err != nil {
		t.Errorf("dispatch for unknown (type, subtype) returned %v, want nil no-op", err)
	}
	if _, err := runConnectorProbe(t.Context(), logger, "knowledge_base", "bedrock", "not-a-map", false); err != nil {
		t.Errorf("dispatch with non-map values returned %v, want nil no-op", err)
	}
	if _, err := runConnectorProbe(t.Context(), nil, "knowledge_base", "bedrock", values, false); err != nil {
		t.Errorf("dispatch with nil logger returned %v, want nil", err)
	}
}
