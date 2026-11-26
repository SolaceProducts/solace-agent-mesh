// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

var PlatformSecretFieldLabels = map[string][]string{
	"api_key":                       {"API Key"},
	"auth_apikey_value":             {"API Key Value"},
	"auth_http_basic_password":      {"Password"},
	"auth_http_bearer_token":        {"Bearer Token"},
	"auth_oauth_client_secret":      {"Client Secret"},
	"aws_access_key_id":             {"AWS Access Key", "AWS Access Key ID"},
	"aws_secret_access_key":         {"AWS Secret Access Key", "AWS Secret Key"},
	"broker_password":               {"Password"},
	"external_id":                   {"External ID"},
	"file_s3_key":                   {"Specification File S3 Key"},
	"oauth_google_client_secret":    {"Google Client Secret"},
	"oauth_google_refresh_token":    {"Google Refresh Token"},
	"oauth_microsoft_client_secret": {"Azure Client Secret"},
	"password":                      {"Password"},
	"slack_bot_token":               {"Bot Token"},
	"smtp_password":                 {"SMTP Password"},
	"smtp_password_login":           {"SMTP Password"},
}

var A2AAgentSecretFieldLabels = map[string][]string{
	"apiKey":       {"API Key"},
	"clientSecret": {"Client Secret"},
	"password":     {"Password"},
	"token":        {"Bearer Token"},
}
