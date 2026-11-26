// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bedrockkb

import (
	"context"
	"os"
	"strings"
	"testing"

	bartypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/types"
)

func TestAuthFromConfig(t *testing.T) {
	config := map[string]any{
		"region":                "us-west-2",
		"aws_access_key_id":     "AKIAIOSFODNN7EXAMPLE",
		"aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"role_arn":              "arn:aws:iam::123456789012:role/MyRole",
		"external_id":           "ext-123",
		"session_name":          "my-session",
	}

	auth := authFromConfig(config)

	if auth.region != "us-west-2" {
		t.Errorf("region = %q, want us-west-2", auth.region)
	}
	if auth.accessKeyID != "AKIAIOSFODNN7EXAMPLE" {
		t.Errorf("accessKeyID = %q, want AKIAIOSFODNN7EXAMPLE", auth.accessKeyID)
	}
	if auth.secretAccessKey != "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" {
		t.Errorf("secretAccessKey mismatch")
	}
	if auth.roleARN != "arn:aws:iam::123456789012:role/MyRole" {
		t.Errorf("roleARN = %q", auth.roleARN)
	}
	if auth.externalID != "ext-123" {
		t.Errorf("externalID = %q", auth.externalID)
	}
	if auth.sessionName != "my-session" {
		t.Errorf("sessionName = %q", auth.sessionName)
	}
}

func TestAuthFromConfig_Defaults(t *testing.T) {
	config := map[string]any{}

	auth := authFromConfig(config)

	if auth.region != "us-east-1" {
		t.Errorf("default region = %q, want us-east-1", auth.region)
	}
	if auth.accessKeyID != "" {
		t.Errorf("accessKeyID should be empty, got %q", auth.accessKeyID)
	}
	if auth.roleARN != "" {
		t.Errorf("roleARN should be empty, got %q", auth.roleARN)
	}
}

func TestConfigString(t *testing.T) {
	config := map[string]any{
		"key1": "value1",
		"key2": 42,
	}

	if got := configString(config, "key1", "default"); got != "value1" {
		t.Errorf("configString(key1) = %q, want value1", got)
	}
	if got := configString(config, "missing", "default"); got != "default" {
		t.Errorf("configString(missing) = %q, want default", got)
	}
	if got := configString(config, "key2", "default"); got != "default" {
		t.Errorf("configString(key2 int) = %q, want default (wrong type)", got)
	}
}

func TestConfigInt(t *testing.T) {
	config := map[string]any{
		"int_val":   5,
		"float_val": float64(10),
		"str_val":   "not a number",
	}

	if got := configInt(config, "int_val", 0); got != 5 {
		t.Errorf("configInt(int_val) = %d, want 5", got)
	}
	if got := configInt(config, "float_val", 0); got != 10 {
		t.Errorf("configInt(float_val) = %d, want 10", got)
	}
	if got := configInt(config, "str_val", 99); got != 99 {
		t.Errorf("configInt(str_val) = %d, want 99 (default)", got)
	}
	if got := configInt(config, "missing", 42); got != 42 {
		t.Errorf("configInt(missing) = %d, want 42 (default)", got)
	}
}

func withTestExitHandler(t *testing.T) (func() error, func()) {
	t.Helper()
	original := exitOnConfigError
	var captured error
	exitOnConfigError = func(err error) {
		captured = err
	}
	return func() error { return captured }, func() { exitOnConfigError = original }
}

func TestDynamicSchema_NoKBID(t *testing.T) {
	getErr, restore := withTestExitHandler(t)
	defer restore()

	config := map[string]any{
		"region": "us-east-1",
	}
	result := dynamicSchema(config)
	if result != nil {
		t.Errorf("expected nil override when kb_id is missing, got %+v", result)
	}
	if err := getErr(); err == nil || !strings.Contains(err.Error(), "kb_id") {
		t.Errorf("expected kb_id validation error to be surfaced via exitOnConfigError; got: %v", err)
	}
}

func TestDynamicSchema_InvalidConfig_FailsDeployment(t *testing.T) {
	getErr, restore := withTestExitHandler(t)
	defer restore()

	config := map[string]any{
		"kb_id":     "ABCD1234EF",
		"region":    "us-east-1",
		"auth_type": "access_key",
	}
	_ = dynamicSchema(config)

	err := getErr()
	if err == nil {
		t.Fatal("expected validation error for access_key without credentials, got nil")
	}
	if !strings.Contains(err.Error(), "aws_access_key_id") {
		t.Errorf("error should name the missing credential; got: %v", err)
	}
}

func TestDynamicSchema_ValidConfig_NoExit(t *testing.T) {
	getErr, restore := withTestExitHandler(t)
	defer restore()

	config := map[string]any{
		"kb_id":     "ABCD1234EF",
		"region":    "us-east-1",
		"tool_name": "custom_kb",
	}
	_ = dynamicSchema(config)
	if err := getErr(); err != nil {
		t.Errorf("valid config should not trigger exitOnConfigError; got: %v", err)
	}
}

func TestDynamicSchema_ToolNameOverride(t *testing.T) {
	config := map[string]any{
		"kb_id":     "test-kb",
		"region":    "us-east-1",
		"tool_name": "my_custom_kb",
	}

	result := dynamicSchema(config)
	if result == nil {
		t.Fatal("expected non-nil override")
	}
	if result.Name != "my_custom_kb" {
		t.Errorf("Name = %q, want my_custom_kb", result.Name)
	}
}

func TestLoadAWSConfig_IRSAPrefersOverEnvStaticCreds(t *testing.T) {
	t.Setenv(envAWSAccessKeyID, "EXAMPLESTATICKEY")
	t.Setenv(envAWSSecretAccessKey, "EXAMPLESTATICSECRET")
	t.Setenv(envWebIdentityTokenFile, "/tmp/fake-web-identity-token")
	t.Setenv("AWS_ROLE_ARN", "arn:aws:iam::123456789012:role/fake-irsa-role")

	cfg, err := loadAWSConfig(context.Background(), awsAuth{region: "us-east-1"})
	if err != nil {
		t.Fatalf("loadAWSConfig: %v", err)
	}

	creds, credErr := cfg.Credentials.Retrieve(context.Background())
	if credErr == nil && creds.AccessKeyID == "EXAMPLESTATICKEY" {
		t.Fatalf("config fell back to static env creds: %+v", creds)
	}
	if credErr == nil && creds.Source == "StaticCredentials" {
		t.Fatalf("config unexpectedly fell back to static env creds: %+v", creds)
	}
	if credErr != nil && strings.Contains(credErr.Error(), "EXAMPLESTATICKEY") {
		t.Fatalf("static env creds leaked into provider chain: %v", credErr)
	}

	if got := os.Getenv(envAWSAccessKeyID); got != "EXAMPLESTATICKEY" {
		t.Errorf("AWS_ACCESS_KEY_ID not restored: got %q, want EXAMPLESTATICKEY", got)
	}
	if got := os.Getenv(envAWSSecretAccessKey); got != "EXAMPLESTATICSECRET" {
		t.Errorf("AWS_SECRET_ACCESS_KEY not restored: got %q", got)
	}
}

func TestLoadAWSConfig_PodIdentityPrefersOverEnvStaticCreds(t *testing.T) {
	t.Setenv(envAWSAccessKeyID, "EXAMPLESTATICKEY2")
	t.Setenv(envAWSSecretAccessKey, "EXAMPLESTATICSECRET2")
	t.Setenv(envContainerAuthzTokenFile, "/tmp/fake-pod-identity-token")

	cfg, err := loadAWSConfig(context.Background(), awsAuth{region: "us-east-1"})
	if err != nil {
		t.Fatalf("loadAWSConfig: %v", err)
	}

	creds, credErr := cfg.Credentials.Retrieve(context.Background())
	if credErr == nil && creds.AccessKeyID == "EXAMPLESTATICKEY2" {
		t.Fatalf("config fell back to static env creds: %+v", creds)
	}
	if credErr == nil && creds.Source == "StaticCredentials" {
		t.Fatalf("config unexpectedly fell back to static env creds: %+v", creds)
	}

	if got := os.Getenv(envAWSAccessKeyID); got != "EXAMPLESTATICKEY2" {
		t.Errorf("AWS_ACCESS_KEY_ID not restored: got %q", got)
	}
	if got := os.Getenv(envAWSSecretAccessKey); got != "EXAMPLESTATICSECRET2" {
		t.Errorf("AWS_SECRET_ACCESS_KEY not restored: got %q", got)
	}
}

func TestLoadAWSConfig_StaticConfigCredsWin(t *testing.T) {
	t.Setenv(envWebIdentityTokenFile, "/tmp/fake-web-identity-token")
	t.Setenv(envAWSAccessKeyID, "EXAMPLEENVKEY")
	t.Setenv(envAWSSecretAccessKey, "EXAMPLEENVSECRET")

	auth := awsAuth{
		region:          "us-east-1",
		accessKeyID:     "CONFIGKEY",
		secretAccessKey: "CONFIGSECRET",
	}
	cfg, err := loadAWSConfig(context.Background(), auth)
	if err != nil {
		t.Fatalf("loadAWSConfig: %v", err)
	}

	got, err := cfg.Credentials.Retrieve(context.Background())
	if err != nil {
		t.Fatalf("Credentials.Retrieve: %v", err)
	}
	if got.AccessKeyID != "CONFIGKEY" {
		t.Errorf("AccessKeyID = %q, want CONFIGKEY", got.AccessKeyID)
	}
	if got.SecretAccessKey != "CONFIGSECRET" {
		t.Errorf("SecretAccessKey = %q, want CONFIGSECRET", got.SecretAccessKey)
	}

	if os.Getenv(envAWSAccessKeyID) != "EXAMPLEENVKEY" {
		t.Errorf("env AWS_ACCESS_KEY_ID was modified: %q", os.Getenv(envAWSAccessKeyID))
	}
}

func TestLoadAWSConfig_NoIRSAPrefersIMDSOverEnvStaticCreds(t *testing.T) {
	const bogusKey = "placeholder"
	t.Setenv(envAWSAccessKeyID, bogusKey)
	t.Setenv(envAWSSecretAccessKey, bogusKey)
	t.Setenv(envWebIdentityTokenFile, "")
	t.Setenv(envContainerAuthzTokenFile, "")

	cfg, err := loadAWSConfig(context.Background(), awsAuth{region: "us-east-1"})
	if err != nil {
		t.Fatalf("loadAWSConfig: %v", err)
	}

	creds, credErr := cfg.Credentials.Retrieve(context.Background())
	if credErr == nil && creds.AccessKeyID == bogusKey {
		t.Fatalf("env-var creds leaked through to AssumeRole base: %+v", creds)
	}

	if got := os.Getenv(envAWSAccessKeyID); got != bogusKey {
		t.Errorf("AWS_ACCESS_KEY_ID not restored after loadAWSConfig: got %q, want %q", got, bogusKey)
	}
	if got := os.Getenv(envAWSSecretAccessKey); got != bogusKey {
		t.Errorf("AWS_SECRET_ACCESS_KEY not restored after loadAWSConfig: got %q, want %q", got, bogusKey)
	}
}

func TestMaskStaticAWSEnvForIRSA_NoOpWithoutIRSA(t *testing.T) {
	t.Setenv(envAWSAccessKeyID, "KEEPME")
	t.Setenv(envAWSSecretAccessKey, "KEEPSECRET")
	_ = os.Unsetenv(envWebIdentityTokenFile)
	_ = os.Unsetenv(envContainerAuthzTokenFile)

	restore := maskStaticAWSEnvForIRSA()
	if os.Getenv(envAWSAccessKeyID) != "KEEPME" {
		t.Errorf("env var was masked when IRSA absent")
	}
	restore()
}

func TestBuildLocation_S3(t *testing.T) {
	uri := "s3://bucket/key.txt"
	loc := buildLocation(&bartypes.RetrievalResultLocation{
		Type:       bartypes.RetrievalResultLocationTypeS3,
		S3Location: &bartypes.RetrievalResultS3Location{Uri: &uri},
	})
	if loc["type"] != "S3" {
		t.Errorf("type = %v, want S3", loc["type"])
	}
	s3, ok := loc["s3Location"].(map[string]any)
	if !ok {
		t.Fatalf("s3Location missing: %+v", loc)
	}
	if s3["uri"] != uri {
		t.Errorf("uri = %v, want %q", s3["uri"], uri)
	}
}

func TestBuildLocation_Confluence(t *testing.T) {
	url := "https://example.atlassian.net/wiki/spaces/X/pages/123"
	loc := buildLocation(&bartypes.RetrievalResultLocation{
		Type:               "CONFLUENCE",
		ConfluenceLocation: &bartypes.RetrievalResultConfluenceLocation{Url: &url},
	})
	conf, ok := loc["confluenceLocation"].(map[string]any)
	if !ok {
		t.Fatalf("confluenceLocation missing: %+v", loc)
	}
	if conf["url"] != url {
		t.Errorf("url = %v, want %q", conf["url"], url)
	}
}

func TestBuildLocation_Salesforce(t *testing.T) {
	url := "https://example.my.salesforce.com"
	loc := buildLocation(&bartypes.RetrievalResultLocation{
		Type:               "SALESFORCE",
		SalesforceLocation: &bartypes.RetrievalResultSalesforceLocation{Url: &url},
	})
	sf, ok := loc["salesforceLocation"].(map[string]any)
	if !ok {
		t.Fatalf("salesforceLocation missing: %+v", loc)
	}
	if sf["url"] != url {
		t.Errorf("url mismatch")
	}
}

func TestBuildLocation_SharePoint(t *testing.T) {
	url := "https://example.sharepoint.com/sites/X"
	loc := buildLocation(&bartypes.RetrievalResultLocation{
		Type:               "SHAREPOINT",
		SharePointLocation: &bartypes.RetrievalResultSharePointLocation{Url: &url},
	})
	sp, ok := loc["sharePointLocation"].(map[string]any)
	if !ok {
		t.Fatalf("sharePointLocation missing: %+v", loc)
	}
	if sp["url"] != url {
		t.Errorf("url mismatch")
	}
}

func TestBuildLocation_Web(t *testing.T) {
	url := "https://example.com/page"
	loc := buildLocation(&bartypes.RetrievalResultLocation{
		Type:        "WEB",
		WebLocation: &bartypes.RetrievalResultWebLocation{Url: &url},
	})
	web, ok := loc["webLocation"].(map[string]any)
	if !ok {
		t.Fatalf("webLocation missing: %+v", loc)
	}
	if web["url"] != url {
		t.Errorf("url mismatch")
	}
}

func TestBuildLocation_Nil(t *testing.T) {
	if loc := buildLocation(nil); loc != nil {
		t.Errorf("buildLocation(nil) = %+v, want nil", loc)
	}
}

func TestValidateConfig_MissingKBID(t *testing.T) {
	err := validateConfig(map[string]any{
		"region":    "us-east-1",
		"auth_type": "iam",
	})
	if err == nil {
		t.Fatal("expected error for missing kb_id, got nil")
	}
	if !strings.Contains(err.Error(), "kb_id") {
		t.Errorf("error should name the missing field; got: %v", err)
	}
}

func TestValidateConfig_AccessKeyModeRequiresCredentials(t *testing.T) {
	err := validateConfig(map[string]any{
		"kb_id":     "ABCD1234EF",
		"region":    "us-east-1",
		"auth_type": "access_key",
	})
	if err == nil {
		t.Fatal("expected error for access_key auth without credentials, got nil")
	}
	if !strings.Contains(err.Error(), "aws_access_key_id") {
		t.Errorf("error should name aws_access_key_id; got: %v", err)
	}

	err = validateConfig(map[string]any{
		"kb_id":             "ABCD1234EF",
		"region":            "us-east-1",
		"auth_type":         "access_key",
		"aws_access_key_id": "AKIAIOSFODNN7EXAMPLE",
	})
	if err == nil {
		t.Fatal("expected error for missing aws_secret_access_key, got nil")
	}
	if !strings.Contains(err.Error(), "aws_secret_access_key") {
		t.Errorf("error should name aws_secret_access_key; got: %v", err)
	}
}

func TestValidateConfig_InvalidAuthType(t *testing.T) {
	err := validateConfig(map[string]any{
		"kb_id":     "ABCD1234EF",
		"region":    "us-east-1",
		"auth_type": "mystery",
	})
	if err == nil {
		t.Fatal("expected error for unknown auth_type, got nil")
	}
	if !strings.Contains(err.Error(), "auth_type") {
		t.Errorf("error should name the field; got: %v", err)
	}
}

func TestValidateConfig_IAMModeIsDefault(t *testing.T) {
	if err := validateConfig(map[string]any{
		"kb_id":  "ABCD1234EF",
		"region": "us-east-1",
	}); err != nil {
		t.Errorf("default auth_type=iam should validate; got: %v", err)
	}
}

func TestBedrockKBConfigSchema_ContainsRequiredFields(t *testing.T) {
	wantRequired := map[string]bool{
		"kb_id":  false,
		"region": false,
	}
	for _, field := range bedrockKBConfigSchema {
		if _, ok := wantRequired[field.Key]; ok && field.Required {
			wantRequired[field.Key] = true
		}
	}
	for key, ok := range wantRequired {
		if !ok {
			t.Errorf("config schema missing required field %q", key)
		}
	}
}
