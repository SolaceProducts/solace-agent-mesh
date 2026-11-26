// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package configbuilder

import (
	"fmt"
	"os"

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
	"gopkg.in/yaml.v3"
)

const (
	DefaultRoleDefinitionsPath = "config/auth/role-to-scope-definitions.yaml"
	DefaultUserRolesPath       = "config/auth/user-to-role-assignments.yaml"
)

func AppConfig(name string, appConfig map[string]any) map[string]any {
	return map[string]any{
		"apps": []map[string]any{{
			"name":       name,
			"app_config": appConfig,
		}},
	}
}

func resolveEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func ModelBlock() map[string]any {
	return map[string]any{
		"model":    resolveEnv("LLM_SERVICE_GENERAL_MODEL_NAME", ""),
		"api_base": resolveEnv("LLM_SERVICE_ENDPOINT", ""),
		"api_key":  resolveEnv("LLM_SERVICE_API_KEY", ""),
	}
}

func SessionBlock(envSuffix string) map[string]any {
	persistenceType := resolveEnv("PERSISTENCE_TYPE", "sql")
	dataDir := resolveEnv("SAM_DATA_DIR", "/tmp/sam-data")
	dbURL := resolveEnv("DATABASE_URL", fmt.Sprintf("sqlite:///%s/agent_%s.db", dataDir, envSuffix))
	return map[string]any{
		"type":         persistenceType,
		"database_url": dbURL,
	}
}

func GatewaySessionBlock(envSuffix string) map[string]any {
	persistenceType := resolveEnv("PERSISTENCE_TYPE", "sql")
	dataDir := resolveEnv("SAM_DATA_DIR", "/tmp/sam-data")
	dbURL := resolveEnv("WEB_UI_GATEWAY_DATABASE_URL", fmt.Sprintf("sqlite:///%s/gateway_%s.db", dataDir, envSuffix))
	return map[string]any{
		"type":         persistenceType,
		"database_url": dbURL,
	}
}

func ArtifactBlock() map[string]any {
	bucket := objectstore.ResolveBucket("ARTIFACT_BUCKET_NAME")
	switch DetectObjectStorageType() {
	case "s3":
		return map[string]any{
			"type":        "s3",
			"bucket_name": bucket,
			"region":      resolveEnv("S3_REGION", "us-east-1"),
		}
	case "azure":
		return map[string]any{
			"type":           "azure",
			"container_name": bucket,
			"account_name":   resolveEnv("AZURE_STORAGE_ACCOUNT_NAME", ""),
			"account_key":    resolveEnv("AZURE_STORAGE_ACCOUNT_KEY", ""),
		}
	case "gcs":
		return map[string]any{
			"type":        "gcs",
			"bucket_name": bucket,
			"project":     resolveEnv("GCS_PROJECT", ""),
		}
	default:
		return map[string]any{
			"type":      resolveEnv("ARTIFACT_SERVICE_TYPE", "filesystem"),
			"base_path": resolveEnv("ARTIFACT_BASE_PATH", "/tmp/samv2"),
		}
	}
}

func DetectObjectStorageType() string {
	switch objectstore.StorageType() {
	case "s3":
		return "s3"
	case "azure":
		return "azure"
	case "gcs":
		return "gcs"
	}
	return "none"
}

func ToControlPayloads(config map[string]any) ([]map[string]any, error) {
	appsRaw, ok := config["apps"]
	if !ok {
		return nil, fmt.Errorf("config has no 'apps' key")
	}

	apps, ok := appsRaw.([]map[string]any)
	if !ok {
		return nil, fmt.Errorf("apps is not []map[string]any")
	}

	var payloads []map[string]any
	for _, app := range apps {
		appConfig, ok := app["app_config"].(map[string]any)
		if !ok {
			continue
		}
		name, _ := app["name"].(string)
		kind := schema.DetectComponentType(appConfig)

		payloads = append(payloads, map[string]any{
			"kind":       kind,
			"name":       name,
			"app_config": appConfig,
		})
	}

	if len(payloads) == 0 {
		return nil, fmt.Errorf("no valid app entries found in config")
	}
	return payloads, nil
}

func ParseYAMLToControlPayloads(yamlBytes []byte) ([]map[string]any, error) {
	parsed, err := schema.ParseAppsConfig(yamlBytes)
	if err != nil {
		return nil, fmt.Errorf("parse YAML: %w", err)
	}

	var payloads []map[string]any
	for _, app := range parsed {
		appConfig, ok := app["app_config"].(map[string]any)
		if !ok {
			continue
		}
		name, _ := app["name"].(string)
		kind := schema.DetectComponentType(appConfig)
		payloads = append(payloads, map[string]any{
			"kind":       kind,
			"name":       name,
			"app_config": appConfig,
		})
	}
	if len(payloads) == 0 {
		return nil, fmt.Errorf("no valid app entries found in YAML")
	}
	return payloads, nil
}

func MarshalYAML(config map[string]any) (string, error) {
	out, err := yaml.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("marshal YAML: %w", err)
	}
	return string(out), nil
}

func SetOrEnvVar(m map[string]any, field string, values map[string]any, envVar string) {
	if v, ok := values[field].(string); ok && v != "" {
		m[field] = v
	} else {
		m[field] = resolveEnv(envVar, "")
	}
}

func SetOrEnvVarFrom(m map[string]any, outputField string, values map[string]any, inputField string, envVar string) {
	if v, ok := values[inputField].(string); ok && v != "" {
		m[outputField] = v
	} else {
		m[outputField] = resolveEnv(envVar, "")
	}
}

func ResolveEnv(key, fallback string) string {
	return resolveEnv(key, fallback)
}

func SetIfPresent(m map[string]any, field string, values map[string]any) {
	v, ok := values[field]
	if !ok {
		return
	}
	switch val := v.(type) {
	case string:
		if val != "" {
			m[field] = val
		}
	case bool:
		m[field] = val
	case float64:
		m[field] = val
	default:
		m[field] = v
	}
}

func SetKeyIfPresent(m map[string]any, field string, values map[string]any) {
	if v, ok := values[field]; ok {
		m[field] = v
	}
}

func GetIntOrDefault(values map[string]any, field string, defaultVal int) int {
	if v, ok := values[field]; ok {
		if n, ok := v.(float64); ok {
			return int(n)
		}
	}
	return defaultVal
}
