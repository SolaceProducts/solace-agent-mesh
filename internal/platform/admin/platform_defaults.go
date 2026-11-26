// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"os"

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
)

type PlatformDefaults struct {
	BrokerDevMode          string
	BrokerURL              string
	BrokerUsername         string
	BrokerVPN              string
	BrokerTemporaryQueue   string
	Model                  string
	ModelAPIBase           string
	SessionType            string
	SessionDefaultBehavior string
	ArtifactType           string
	ArtifactBasePath       string
	ArtifactBucketName     string
	ArtifactEndpointURL    string
	ArtifactScope          string
	LogStdoutLevel         string
	LogFileLevel           string
	LogFile                string
}

func ReadPlatformDefaults() PlatformDefaults {
	return PlatformDefaults{
		BrokerDevMode:          envOr("SOLACE_DEV_MODE", "false"),
		BrokerURL:              envOr("SOLACE_BROKER_URL", "ws://localhost:8080"),
		BrokerUsername:         envOr("SOLACE_BROKER_USERNAME", "default"),
		BrokerVPN:              envOr("SOLACE_BROKER_VPN", "default"),
		BrokerTemporaryQueue:   envOr("USE_TEMPORARY_QUEUES", "true"),
		Model:                  os.Getenv("LLM_SERVICE_GENERAL_MODEL_NAME"),
		ModelAPIBase:           os.Getenv("LLM_SERVICE_ENDPOINT"),
		SessionType:            envOr("PERSISTENCE_TYPE", "sql"),
		SessionDefaultBehavior: "PERSISTENT",
		ArtifactType:           envOr("ARTIFACT_SERVICE_TYPE", "filesystem"),
		ArtifactBasePath:       envOr("ARTIFACT_BASE_PATH", "/tmp/samv2"),
		ArtifactBucketName:     objectstore.ResolveBucket("ARTIFACT_BUCKET_NAME"),
		ArtifactEndpointURL:    os.Getenv("S3_ENDPOINT_URL"),
		ArtifactScope:          "namespace",
		LogStdoutLevel:         "INFO",
		LogFileLevel:           "INFO",
		LogFile:                "{{agentName}}.log",
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
