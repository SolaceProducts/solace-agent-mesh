// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

func BuildResource(defaultServiceName string) (*resource.Resource, error) {
	baseAttrs := []attribute.KeyValue{
		semconv.ServiceNameKey.String(defaultServiceName),
		semconv.ServiceVersionKey.String(samversion.Version),
	}
	if instance := serviceInstanceID(); instance != "" {
		baseAttrs = append(baseAttrs, semconv.ServiceInstanceIDKey.String(instance))
	}
	base := resource.NewSchemaless(baseAttrs...)

	envRes, err := resource.New(context.Background(), resource.WithFromEnv())
	if err != nil && !errors.Is(err, resource.ErrPartialResource) {
		return nil, fmt.Errorf("detect resource from env: %w", err)
	}
	merged, err := resource.Merge(base, envRes)
	if err != nil {
		return nil, fmt.Errorf("merge env resource: %w", err)
	}

	ddEnv := []struct {
		envVar string
		key    attribute.Key
	}{
		{"DD_SERVICE", semconv.ServiceNameKey},
		{"DD_ENV", semconv.DeploymentEnvironmentKey},
		{"DD_VERSION", semconv.ServiceVersionKey},
	}
	var ddAttrs []attribute.KeyValue
	for _, m := range ddEnv {
		if v := strings.TrimSpace(os.Getenv(m.envVar)); v != "" {
			ddAttrs = append(ddAttrs, m.key.String(v))
		}
	}
	if len(ddAttrs) == 0 {
		return merged, nil
	}

	final, err := resource.Merge(merged, resource.NewSchemaless(ddAttrs...))
	if err != nil {
		return nil, fmt.Errorf("merge datadog resource: %w", err)
	}
	return final, nil
}

func serviceInstanceID() string {
	host, err := os.Hostname()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(host)
}
