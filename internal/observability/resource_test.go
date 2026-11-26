// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"os"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

func clearResourceEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"OTEL_SERVICE_NAME", "OTEL_RESOURCE_ATTRIBUTES", "DD_SERVICE", "DD_ENV", "DD_VERSION"} {
		t.Setenv(k, "")
	}
}

func resourceAttr(t *testing.T, res *resource.Resource, key attribute.Key) (string, bool) {
	t.Helper()
	for _, kv := range res.Attributes() {
		if kv.Key == key {
			return kv.Value.AsString(), true
		}
	}
	return "", false
}

func TestBuildResource(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("os.Hostname: %v", err)
	}
	tests := []struct {
		name         string
		env          map[string]string
		wantSvc      string
		wantVer      string
		wantEnv      string
		wantInstance string
	}{
		{
			name:         "defaults when no env",
			wantSvc:      "sam-awe",
			wantVer:      samversion.Version,
			wantInstance: host,
		},
		{
			name:         "OTEL_SERVICE_NAME overrides default",
			env:          map[string]string{"OTEL_SERVICE_NAME": "otel-svc"},
			wantSvc:      "otel-svc",
			wantVer:      samversion.Version,
			wantInstance: host,
		},
		{
			name:         "OTEL_RESOURCE_ATTRIBUTES sets deployment.environment",
			env:          map[string]string{"OTEL_RESOURCE_ATTRIBUTES": "deployment.environment=staging"},
			wantSvc:      "sam-awe",
			wantVer:      samversion.Version,
			wantEnv:      "staging",
			wantInstance: host,
		},
		{
			name:         "OTEL_RESOURCE_ATTRIBUTES overrides the host name as service.instance.id",
			env:          map[string]string{"OTEL_RESOURCE_ATTRIBUTES": "service.instance.id=gwe-pod-b"},
			wantSvc:      "sam-awe",
			wantVer:      samversion.Version,
			wantInstance: "gwe-pod-b",
		},
		{
			name:         "DD_* wins over OTel env and default",
			env:          map[string]string{"OTEL_SERVICE_NAME": "otel-svc", "DD_SERVICE": "dd-svc", "DD_ENV": "prod", "DD_VERSION": "1.2.3"},
			wantSvc:      "dd-svc",
			wantVer:      "1.2.3",
			wantEnv:      "prod",
			wantInstance: host,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearResourceEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			res, err := BuildResource("sam-awe")
			if err != nil {
				t.Fatalf("BuildResource: %v", err)
			}
			if got, _ := resourceAttr(t, res, semconv.ServiceNameKey); got != tt.wantSvc {
				t.Errorf("service.name = %q, want %q", got, tt.wantSvc)
			}
			if got, _ := resourceAttr(t, res, semconv.ServiceVersionKey); got != tt.wantVer {
				t.Errorf("service.version = %q, want %q", got, tt.wantVer)
			}
			if got, _ := resourceAttr(t, res, semconv.ServiceInstanceIDKey); got != tt.wantInstance {
				t.Errorf("service.instance.id = %q, want %q", got, tt.wantInstance)
			}
			gotEnv, ok := resourceAttr(t, res, semconv.DeploymentEnvironmentKey)
			switch {
			case tt.wantEnv == "" && ok:
				t.Errorf("deployment.environment = %q, want absent", gotEnv)
			case tt.wantEnv != "" && gotEnv != tt.wantEnv:
				t.Errorf("deployment.environment = %q, want %q", gotEnv, tt.wantEnv)
			}
		})
	}
}

func TestWithResource_FlowsToExport(t *testing.T) {
	clearResourceEnv(t)

	res, err := BuildResource("sam-gateway")
	if err != nil {
		t.Fatalf("BuildResource: %v", err)
	}

	reader := sdkmetric.NewManualReader()
	reg, err := NewRegistry(
		Config{Enabled: true, MetricPrefix: "test"},
		nil,
		WithResource(res),
		WithAdditionalReader(reader),
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	reg.Counter("operation.duration", 1)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	found := false
	for _, kv := range rm.Resource.Attributes() {
		if kv.Key == semconv.ServiceNameKey && kv.Value.AsString() == "sam-gateway" {
			found = true
		}
	}
	if !found {
		t.Errorf("exported resource missing service.name=sam-gateway; got %v", rm.Resource.Attributes())
	}
}
