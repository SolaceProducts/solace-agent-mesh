// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	isolatedJobNamePrefix    = "str-iso-"
	isolatedContainerName    = "tool"
	isolatedOwnContainerName = "str"
	isolatedHashHexLen       = 16
	isolatedJobTTLSeconds    = 3600
	isolatedRunAsUser        = 10001
	isolatedLabelManagedBy   = "app.kubernetes.io/managed-by"
	isolatedManagedByValue   = "sam-str"
	isolatedLabelInvoke      = "sam-str-invocation"
	isolatedLabelTool        = "sam-str-tool"
	IsolatedToolsURLPath     = "/isolated/tools/"

	isolatedWorkDir     = "/work"
	isolatedArgsDir     = "/etc/sam/args"
	isolatedArgsFile    = "runner_args.json"
	isolatedSecretKey   = isolatedArgsFile
	maxLabelValueLength = 63
)

var invalidLabelChars = regexp.MustCompile(`[^a-z0-9._-]`)

func isolatedInvocationHash(namespace, tool, taskID string) string {
	sum := sha256.Sum256([]byte(namespace + "\x00" + tool + "\x00" + taskID))
	return hex.EncodeToString(sum[:])[:isolatedHashHexLen]
}

func isolatedJobName(namespace, tool, taskID string) string {
	return isolatedJobNamePrefix + isolatedInvocationHash(namespace, tool, taskID)
}

func sanitizeLabelValue(value string) string {
	value = invalidLabelChars.ReplaceAllString(strings.ToLower(value), "-")
	if len(value) > maxLabelValueLength {
		value = value[:maxLabelValueLength]
	}
	return strings.Trim(value, "-_.")
}

type isolatedInvocation struct {
	jobName        string
	hash           string
	tool           string
	timeoutSeconds int
	resources      IsolatedResources
	toolURL        string
	runnerArgs     []byte

	deadlineSeconds int
}

func (i isolatedInvocation) labels() map[string]string {
	return map[string]string{
		isolatedLabelManagedBy: isolatedManagedByValue,
		isolatedLabelInvoke:    i.hash,
		isolatedLabelTool:      sanitizeLabelValue(i.tool),
	}
}

func isolatedRunnerArgs(args *runnerArgs) *runnerArgs {
	rewritten := *args
	rewritten.ResultFile = isolatedWorkDir + "/result.json"
	rewritten.OutputDir = isolatedWorkDir + "/output"
	rewritten.StatusPipe = isolatedWorkDir + "/status.pipe"
	rewritten.IPCSocket = ""
	rewritten.ArtifactPaths = map[string]string{}
	rewritten.ArtifactMetadata = map[string]map[string]any{}
	rewritten.VolumeMounts = nil
	return &rewritten
}

func isolatedToolURL(packageURL, toolsetsDir, executable string) (string, error) {
	root, err := filepath.Abs(toolsetsDir)
	if err != nil {
		return "", err
	}
	exe, err := filepath.Abs(executable)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, exe)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errIsolatedExecutableOutsideToolsets
	}
	segments := strings.Split(filepath.ToSlash(rel), "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return packageURL + IsolatedToolsURLPath + strings.Join(segments, "/"), nil
}

const (
	isolatedStatusPrefix = "@@SAM_STATUS "
	isolatedResultBegin  = "@@SAM_RESULT_BEGIN"
	isolatedResultEnd    = "@@SAM_RESULT_END"
	isolatedOutputMarker = "@@SAM_OUTPUT_FILES "
)

const isolatedShim = `set -eu
cd ` + isolatedWorkDir + `
mkdir -p tool output home input
curl -fsS --retry 3 --retry-delay 1 --connect-timeout 5 -o tool/exe "$TOOL_URL"
chmod 0755 tool/exe
mkfifo status.pipe
( while IFS= read -r line; do printf '` + isolatedStatusPrefix + `%s\n' "$line"; done <> status.pipe ) &
relay=$!
trap 'exit 143' TERM INT
rc=0
./tool/exe ` + isolatedArgsDir + `/` + isolatedArgsFile + ` &
wait "$!" || rc=$?
sleep 1
kill "$relay" 2>/dev/null || true
produced=$(ls output | wc -l)
if [ "$produced" -gt 0 ]; then echo "` + isolatedOutputMarker + `$produced"; fi
echo ` + isolatedResultBegin + `
if [ -f result.json ]; then cat result.json; fi
echo
echo ` + isolatedResultEnd + `
exit "$rc"
`

func buildIsolatedSecret(namespace string, inv isolatedInvocation, jobUID string) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      inv.jobName,
			"namespace": namespace,
			"labels":    inv.labels(),
			"ownerReferences": []any{map[string]any{
				"apiVersion": "batch/v1",
				"kind":       "Job",
				"name":       inv.jobName,
				"uid":        jobUID,
			}},
		},
		"type":       "Opaque",
		"stringData": map[string]any{isolatedSecretKey: string(inv.runnerArgs)},
	}
}

func buildIsolatedJob(cfg IsolatedConfig, inv isolatedInvocation) map[string]any {
	labels := inv.labels()
	podSpec := map[string]any{
		"restartPolicy":                "Never",
		"automountServiceAccountToken": false,
		"enableServiceLinks":           false,
		"securityContext": map[string]any{
			"runAsNonRoot":   true,
			"runAsUser":      isolatedRunAsUser,
			"runAsGroup":     isolatedRunAsUser,
			"seccompProfile": map[string]any{"type": "RuntimeDefault"},
		},
		"containers": []any{map[string]any{
			"name":            isolatedContainerName,
			"image":           cfg.Image,
			"imagePullPolicy": "IfNotPresent",
			"command":         []any{"/bin/sh", "-c", isolatedShim},
			"env": []any{
				map[string]any{"name": "TOOL_URL", "value": inv.toolURL},
				map[string]any{"name": "HOME", "value": isolatedWorkDir + "/home"},
				map[string]any{"name": "TMPDIR", "value": "/tmp"},
				map[string]any{"name": "_SAM_SANDBOX_LIGHT", "value": "1"},
			},
			"securityContext": map[string]any{
				"allowPrivilegeEscalation": false,
				"readOnlyRootFilesystem":   true,
				"runAsNonRoot":             true,
				"runAsUser":                isolatedRunAsUser,
				"capabilities":             map[string]any{"drop": []any{"ALL"}},
			},
			"resources": isolatedResourceRequirements(inv.resources),
			"volumeMounts": []any{
				map[string]any{"name": "work", "mountPath": isolatedWorkDir},
				map[string]any{"name": "tmp", "mountPath": "/tmp"},
				map[string]any{"name": "args", "mountPath": isolatedArgsDir, "readOnly": true},
			},
		}},
		"volumes": []any{
			map[string]any{"name": "work", "emptyDir": map[string]any{"sizeLimit": inv.resources.EphemeralStorage}},
			map[string]any{"name": "tmp", "emptyDir": map[string]any{}},
			map[string]any{"name": "args", "secret": map[string]any{"secretName": inv.jobName}},
		},
	}
	if cfg.RuntimeClass != "" {
		podSpec["runtimeClassName"] = cfg.RuntimeClass
	}
	if len(cfg.NodeSelector) > 0 {
		podSpec["nodeSelector"] = cfg.NodeSelector
	}
	if len(cfg.Tolerations) > 0 {
		podSpec["tolerations"] = cfg.Tolerations
	}
	if cfg.ImagePullSecret != "" {
		podSpec["imagePullSecrets"] = []any{map[string]any{"name": cfg.ImagePullSecret}}
	}
	return map[string]any{
		"apiVersion": "batch/v1",
		"kind":       "Job",
		"metadata": map[string]any{
			"name":      inv.jobName,
			"namespace": cfg.Namespace,
			"labels":    labels,
		},
		"spec": map[string]any{
			"backoffLimit":            0,
			"activeDeadlineSeconds":   inv.deadlineSeconds,
			"ttlSecondsAfterFinished": isolatedJobTTLSeconds,
			"template": map[string]any{
				"metadata": map[string]any{"labels": labels},
				"spec":     podSpec,
			},
		},
	}
}

func isolatedResourceRequirements(res IsolatedResources) map[string]any {
	quantities := map[string]any{
		"cpu":               res.CPU,
		"memory":            res.Memory,
		"ephemeral-storage": res.EphemeralStorage,
	}
	return map[string]any{"requests": quantities, "limits": quantities}
}

func marshalIsolatedArgs(args *runnerArgs) ([]byte, error) {
	data, err := json.Marshal(isolatedRunnerArgs(args))
	if err != nil {
		return nil, fmt.Errorf("marshal isolated runner args: %w", err)
	}
	return data, nil
}
