// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package doctor

import (
	"os"
)

type Env struct {
	DoctorContext string
	SkipChecks    string

	SolaceBrokerURL      string
	SolaceBrokerVPN      string
	SolaceBrokerUsername string
	SolaceBrokerPassword string
	SolaceDevMode        string
	SolaceTrustStore     string

	LLMServiceEndpoint string
	LLMServiceAPIKey   string

	PlatformDatabaseURL     string
	OrchestratorDatabaseURL string
	WebUIGatewayDatabaseURL string

	ObjectStorageType            string
	S3BucketName                 string
	ConnectorSpecBucketName      string
	S3Region                     string
	AWSRegion                    string
	S3EndpointURL                string
	AWSAccessKeyID               string
	AWSSecretAccessKey           string
	AzureStorageAccountName      string
	AzureStorageAccountKey       string
	AzureStorageConnectionString string
	GoogleApplicationCredentials string

	TLSCertPath string
	TLSCert     string
	TLSDNSName  string

	AuthenticationServer  string
	AuthenticationEnabled string

	FastAPIPort     string
	PlatformAPIPort string
}

func LoadEnv() Env {
	return Env{
		DoctorContext: os.Getenv("SAM_DOCTOR_CONTEXT"),
		SkipChecks:    os.Getenv("SAM_DOCTOR_SKIP_CHECKS"),

		SolaceBrokerURL:      os.Getenv("SOLACE_BROKER_URL"),
		SolaceBrokerVPN:      os.Getenv("SOLACE_BROKER_VPN"),
		SolaceBrokerUsername: os.Getenv("SOLACE_BROKER_USERNAME"),
		SolaceBrokerPassword: os.Getenv("SOLACE_BROKER_PASSWORD"),
		SolaceDevMode:        os.Getenv("SOLACE_DEV_MODE"),
		SolaceTrustStore:     os.Getenv("TRUST_STORE"),

		LLMServiceEndpoint: os.Getenv("LLM_SERVICE_ENDPOINT"),
		LLMServiceAPIKey:   os.Getenv("LLM_SERVICE_API_KEY"),

		PlatformDatabaseURL:     os.Getenv("PLATFORM_DATABASE_URL"),
		OrchestratorDatabaseURL: os.Getenv("ORCHESTRATOR_DATABASE_URL"),
		WebUIGatewayDatabaseURL: os.Getenv("WEB_UI_GATEWAY_DATABASE_URL"),

		ObjectStorageType:            os.Getenv("OBJECT_STORAGE_TYPE"),
		S3BucketName:                 os.Getenv("S3_BUCKET_NAME"),
		ConnectorSpecBucketName:      os.Getenv("CONNECTOR_SPEC_BUCKET_NAME"),
		S3Region:                     os.Getenv("S3_REGION"),
		AWSRegion:                    os.Getenv("AWS_REGION"),
		S3EndpointURL:                os.Getenv("S3_ENDPOINT_URL"),
		AWSAccessKeyID:               os.Getenv("AWS_ACCESS_KEY_ID"),
		AWSSecretAccessKey:           os.Getenv("AWS_SECRET_ACCESS_KEY"),
		AzureStorageAccountName:      os.Getenv("AZURE_STORAGE_ACCOUNT_NAME"),
		AzureStorageAccountKey:       os.Getenv("AZURE_STORAGE_ACCOUNT_KEY"),
		AzureStorageConnectionString: os.Getenv("AZURE_STORAGE_CONNECTION_STRING"),
		GoogleApplicationCredentials: os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"),

		TLSCertPath: os.Getenv("TLS_CERT_PATH"),
		TLSCert:     os.Getenv("TLS_CERT"),
		TLSDNSName:  os.Getenv("TLS_DNS_NAME"),

		AuthenticationServer:  os.Getenv("AUTHENTICATION_SERVER"),
		AuthenticationEnabled: os.Getenv("AUTHENTICATION_ENABLED"),

		FastAPIPort:     os.Getenv("FASTAPI_PORT"),
		PlatformAPIPort: os.Getenv("PLATFORM_API_PORT"),
	}
}
