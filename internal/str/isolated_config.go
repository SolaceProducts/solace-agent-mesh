// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

const (
	defaultIsolatedCPU       = "1"
	defaultIsolatedMemory    = "2Gi"
	defaultIsolatedEphemeral = "5Gi"

	defaultIsolatedStartTimeout = 2 * time.Minute

	kubeArchLabel = "kubernetes.io/arch"

	serviceAccountNamespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
)

var quantityPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?(m|k|M|G|T|Ki|Mi|Gi|Ti)?$`)

type IsolatedResources struct {
	CPU              string `yaml:"cpu,omitempty"`
	Memory           string `yaml:"memory,omitempty"`
	EphemeralStorage string `yaml:"ephemeral_storage,omitempty"`
}

func (r *IsolatedResources) asMap() map[string]any {
	out := make(map[string]any)
	if r.CPU != "" {
		out["cpu"] = r.CPU
	}
	if r.Memory != "" {
		out["memory"] = r.Memory
	}
	if r.EphemeralStorage != "" {
		out["ephemeral_storage"] = r.EphemeralStorage
	}
	return out
}

func (r *IsolatedResources) withDefaults() IsolatedResources {
	out := IsolatedResources{
		CPU:              defaultIsolatedCPU,
		Memory:           defaultIsolatedMemory,
		EphemeralStorage: defaultIsolatedEphemeral,
	}
	if r == nil {
		return out
	}
	if r.CPU != "" {
		out.CPU = r.CPU
	}
	if r.Memory != "" {
		out.Memory = r.Memory
	}
	if r.EphemeralStorage != "" {
		out.EphemeralStorage = r.EphemeralStorage
	}
	return out
}

func (r *IsolatedResources) validate() error {
	for key, quantity := range r.asMap() {
		if !quantityPattern.MatchString(fmt.Sprint(quantity)) {
			return fmt.Errorf("job_template.%s %q is not a Kubernetes quantity", key, quantity)
		}
	}
	return nil
}

type IsolatedToleration struct {
	Key      string `json:"key,omitempty"`
	Operator string `json:"operator,omitempty"`
	Value    string `json:"value,omitempty"`
	Effect   string `json:"effect,omitempty"`
}

type IsolatedConfig struct {
	Namespace string

	Image string

	PackageURL string

	ImagePullSecret string
	RuntimeClass    string
	NodeSelector    map[string]string
	Tolerations     []IsolatedToleration

	Resources IsolatedResources

	StartTimeout time.Duration

	ToolsetsDir string
}

func parseIsolatedConfig(sandbox config.Config) (IsolatedConfig, error) {
	if sandbox == nil {
		return IsolatedConfig{}, nil
	}
	section := sandbox.GetSection("job_template")
	if section == nil {
		return IsolatedConfig{}, nil
	}
	cfg := IsolatedConfig{
		Namespace:       strings.TrimSpace(section.GetString("namespace")),
		Image:           strings.TrimSpace(section.GetString("image")),
		PackageURL:      strings.TrimRight(strings.TrimSpace(section.GetString("package_url")), "/"),
		ImagePullSecret: strings.TrimSpace(section.GetString("image_pull_secret")),
		RuntimeClass:    strings.TrimSpace(section.GetString("runtime_class")),
		Resources: IsolatedResources{
			CPU:              strings.TrimSpace(section.GetString("cpu")),
			Memory:           strings.TrimSpace(section.GetString("memory")),
			EphemeralStorage: strings.TrimSpace(section.GetString("ephemeral_storage")),
		},
	}
	if err := cfg.Resources.validate(); err != nil {
		return IsolatedConfig{}, err
	}
	startTimeoutSeconds := section.GetInt("start_timeout_seconds")
	if startTimeoutSeconds < 0 {
		return IsolatedConfig{}, fmt.Errorf("job_template.start_timeout_seconds %d is negative", startTimeoutSeconds)
	}
	cfg.StartTimeout = time.Duration(startTimeoutSeconds) * time.Second
	var err error
	if cfg.NodeSelector, err = parseNodeSelector(section.GetString("node_selector")); err != nil {
		return IsolatedConfig{}, err
	}
	if cfg.Tolerations, err = parseTolerations(section.GetRaw("tolerations")); err != nil {
		return IsolatedConfig{}, err
	}
	if cfg.Namespace == "" && sandbox.GetString("mode") == sandboxModeKubernetesJob {
		cfg.Namespace = readPodNamespace()
	}
	return cfg, nil
}

func (c IsolatedConfig) validate() error {
	switch {
	case c.Namespace == "":
		return fmt.Errorf("STR_JOB_NAMESPACE is empty and the pod namespace could not be read")
	case c.PackageURL == "":
		return fmt.Errorf("STR_JOB_PACKAGE_URL is required")
	case c.ToolsetsDir == "":
		return fmt.Errorf("toolsets_dir is required so Job pods can fetch tool files")
	}
	return nil
}

func (c IsolatedConfig) startTimeout() time.Duration {
	if c.StartTimeout <= 0 {
		return defaultIsolatedStartTimeout
	}
	return c.StartTimeout
}

func withOwnArch(selector map[string]string) map[string]string {
	if _, pinned := selector[kubeArchLabel]; pinned {
		return selector
	}
	out := make(map[string]string, len(selector)+1)
	maps.Copy(out, selector)
	out[kubeArchLabel] = runtimeGOARCH()
	return out
}

func readPodNamespace() string {
	data, err := os.ReadFile(serviceAccountNamespaceFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func parseNodeSelector(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	selector := make(map[string]string)
	for _, pair := range strings.Split(raw, ",") {
		key, value, found := strings.Cut(strings.TrimSpace(pair), "=")
		if !found || key == "" {
			return nil, fmt.Errorf("STR_JOB_NODE_SELECTOR entry %q is not key=value", pair)
		}
		selector[key] = value
	}
	return selector, nil
}

func parseTolerations(raw any) ([]IsolatedToleration, error) {
	var encoded []byte
	switch value := raw.(type) {
	case nil:
		return nil, nil
	case string:
		if strings.TrimSpace(value) == "" {
			return nil, nil
		}
		encoded = []byte(value)
	default:
		var err error
		if encoded, err = json.Marshal(value); err != nil {
			return nil, fmt.Errorf("STR_JOB_TOLERATIONS must be a JSON array of tolerations: %w", err)
		}
	}
	var tolerations []IsolatedToleration
	if err := json.Unmarshal(encoded, &tolerations); err != nil {
		return nil, fmt.Errorf("STR_JOB_TOLERATIONS must be a JSON array of tolerations: %w", err)
	}
	return tolerations, nil
}
