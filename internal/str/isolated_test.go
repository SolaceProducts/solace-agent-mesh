// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

func TestParseManifestEntry_ExecutionClass(t *testing.T) {
	entry, err := parseManifestEntry("render", map[string]any{"execution_class": " isolated "})
	if err != nil {
		t.Fatalf("parseManifestEntry: %v", err)
	}
	if entry.ExecutionClass != "isolated" {
		t.Errorf("ExecutionClass = %q, want isolated", entry.ExecutionClass)
	}

	plain, err := parseManifestEntry("render", map[string]any{})
	if err != nil {
		t.Fatalf("parseManifestEntry: %v", err)
	}
	if plain.ExecutionClass != "" {
		t.Errorf("ExecutionClass = %q, want empty for a tool that declares none", plain.ExecutionClass)
	}
}

func TestPackageManifest_ExecutionClassSurvivesMergedManifest(t *testing.T) {
	var manifest packageManifest
	if err := yaml.Unmarshal([]byte("version: 1\nexecution_class: isolated\ntools:\n  render: {}\n"), &manifest); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	entry, err := parseManifestEntry("pkg__render", map[string]any{"execution_class": manifest.ExecutionClass})
	if err != nil {
		t.Fatalf("parseManifestEntry: %v", err)
	}
	if entry.ExecutionClass != "isolated" {
		t.Errorf("ExecutionClass = %q, want isolated", entry.ExecutionClass)
	}
}

func TestIsolatedResources_Defaults(t *testing.T) {
	var unset IsolatedResources
	resolved := unset.withDefaults()
	if resolved.CPU != defaultIsolatedCPU || resolved.Memory != defaultIsolatedMemory || resolved.EphemeralStorage != defaultIsolatedEphemeral {
		t.Errorf("withDefaults = %+v", resolved)
	}
	sized := IsolatedResources{Memory: "6Gi"}
	if got := sized.withDefaults(); got.Memory != "6Gi" || got.CPU != defaultIsolatedCPU {
		t.Errorf("withDefaults = %+v, want memory kept and cpu defaulted", got)
	}
}

func TestIsolatedInvocationHash_StableAndDistinct(t *testing.T) {
	const wantHash = "2662b2bf04fd5651"
	got := isolatedInvocationHash("ns", "pkg__render", "corr-1")
	if got != wantHash {
		t.Errorf("hash = %q, want %q (changing it orphans Jobs of in-flight invocations on upgrade)", got, wantHash)
	}
	if again := isolatedInvocationHash("ns", "pkg__render", "corr-1"); again != got {
		t.Errorf("hash not deterministic: %q vs %q", again, got)
	}
	for name, other := range map[string]string{
		"namespace": isolatedInvocationHash("ns2", "pkg__render", "corr-1"),
		"tool":      isolatedInvocationHash("ns", "pkg__other", "corr-1"),
		"task":      isolatedInvocationHash("ns", "pkg__render", "corr-2"),
		"boundary":  isolatedInvocationHash("n", "spkg__render", "corr-1"),
	} {
		if other == got {
			t.Errorf("hash ignores %s", name)
		}
	}
	if name := isolatedJobName("ns", "pkg__render", "corr-1"); name != "str-iso-"+wantHash {
		t.Errorf("job name = %q", name)
	}
}

func TestSanitizeLabelValue(t *testing.T) {
	cases := map[string]string{
		"pkg__Render Tool":      "pkg__render-tool",
		"-weird-":               "weird",
		strings.Repeat("a", 80): strings.Repeat("a", maxLabelValueLength),
	}
	for in, want := range cases {
		if got := sanitizeLabelValue(in); got != want {
			t.Errorf("sanitizeLabelValue(%q) = %q, want %q", in, got, want)
		}
	}
}

func testIsolatedInvocation() isolatedInvocation {
	return isolatedInvocation{
		jobName:        "str-iso-abc",
		hash:           "abc",
		tool:           "pkg__render",
		timeoutSeconds: 120,
		resources:      IsolatedResources{CPU: "2", Memory: "6Gi", EphemeralStorage: "15Gi"},
		toolURL:        "http://str:8090/isolated/tools/pkg/render",
		runnerArgs:     []byte(`{"tool_name":"render"}`),

		deadlineSeconds: 240,
	}
}

func asJSONMap(t *testing.T, v any) map[string]any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func dig(t *testing.T, m map[string]any, path ...string) any {
	t.Helper()
	var cur any = m
	for _, key := range path {
		next, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("path %v: %T is not a map", path, cur)
		}
		cur = next[key]
	}
	return cur
}

func TestBuildIsolatedJob(t *testing.T) {
	cfg := IsolatedConfig{
		Namespace:       "sam",
		Image:           "str:1",
		RuntimeClass:    "gvisor",
		NodeSelector:    map[string]string{"pool": "tools"},
		Tolerations:     []IsolatedToleration{{Key: "tools", Operator: "Exists", Effect: "NoSchedule"}},
		ImagePullSecret: "regcred",
	}
	job := asJSONMap(t, buildIsolatedJob(cfg, testIsolatedInvocation()))

	if got := dig(t, job, "metadata", "name"); got != "str-iso-abc" {
		t.Errorf("name = %v", got)
	}
	labels := dig(t, job, "metadata", "labels").(map[string]any)
	if labels[isolatedLabelManagedBy] != "sam-str" || labels[isolatedLabelInvoke] != "abc" || labels[isolatedLabelTool] != "pkg__render" {
		t.Errorf("labels = %v", labels)
	}
	spec := dig(t, job, "spec").(map[string]any)
	if spec["backoffLimit"] != float64(0) || spec["activeDeadlineSeconds"] != float64(240) || spec["ttlSecondsAfterFinished"] != float64(3600) {
		t.Errorf("job spec = %v", spec)
	}
	pod := dig(t, job, "spec", "template", "spec").(map[string]any)
	if pod["restartPolicy"] != "Never" || pod["automountServiceAccountToken"] != false {
		t.Errorf("pod spec = %v", pod)
	}
	if pod["runtimeClassName"] != "gvisor" {
		t.Errorf("runtimeClassName = %v", pod["runtimeClassName"])
	}
	if sel := pod["nodeSelector"].(map[string]any); sel["pool"] != "tools" {
		t.Errorf("nodeSelector = %v", sel)
	}
	if len(pod["tolerations"].([]any)) != 1 || len(pod["imagePullSecrets"].([]any)) != 1 {
		t.Errorf("tolerations/imagePullSecrets missing: %v", pod)
	}

	container := pod["containers"].([]any)[0].(map[string]any)
	sc := container["securityContext"].(map[string]any)
	if sc["readOnlyRootFilesystem"] != true || sc["runAsNonRoot"] != true || sc["runAsUser"] != float64(10001) || sc["allowPrivilegeEscalation"] != false {
		t.Errorf("container securityContext = %v", sc)
	}
	if drop := dig(t, sc, "capabilities", "drop").([]any); len(drop) != 1 || drop[0] != "ALL" {
		t.Errorf("capabilities.drop = %v", drop)
	}
	res := container["resources"].(map[string]any)
	for _, kind := range []string{"requests", "limits"} {
		q := res[kind].(map[string]any)
		if q["cpu"] != "2" || q["memory"] != "6Gi" || q["ephemeral-storage"] != "15Gi" {
			t.Errorf("resources.%s = %v", kind, q)
		}
	}
	volumes := pod["volumes"].([]any)
	work := volumes[0].(map[string]any)
	if dig(t, work, "emptyDir", "sizeLimit") != "15Gi" {
		t.Errorf("work volume = %v", work)
	}
	if dig(t, volumes[2].(map[string]any), "secret", "secretName") != "str-iso-abc" {
		t.Errorf("args volume = %v", volumes[2])
	}
	env := container["env"].([]any)[0].(map[string]any)
	if env["name"] != "TOOL_URL" || env["value"] != "http://str:8090/isolated/tools/pkg/render" {
		t.Errorf("env = %v", env)
	}
}

func TestBuildIsolatedJob_OptionalFieldsOmitted(t *testing.T) {
	job := asJSONMap(t, buildIsolatedJob(IsolatedConfig{Namespace: "sam", Image: "str:1"}, testIsolatedInvocation()))
	pod := dig(t, job, "spec", "template", "spec").(map[string]any)
	for _, key := range []string{"runtimeClassName", "nodeSelector", "tolerations", "imagePullSecrets"} {
		if _, present := pod[key]; present {
			t.Errorf("%s should be absent when unset", key)
		}
	}
}

func TestBuildIsolatedSecret(t *testing.T) {
	secret := asJSONMap(t, buildIsolatedSecret("sam", testIsolatedInvocation(), "uid-1"))
	owner := dig(t, secret, "metadata", "ownerReferences").([]any)[0].(map[string]any)
	if owner["kind"] != "Job" || owner["name"] != "str-iso-abc" || owner["uid"] != "uid-1" || owner["apiVersion"] != "batch/v1" {
		t.Errorf("ownerReference = %v", owner)
	}
	if dig(t, secret, "stringData", "runner_args.json") != `{"tool_name":"render"}` {
		t.Errorf("stringData = %v", secret["stringData"])
	}
}

func TestIsolatedRunnerArgs_RewritesPodPaths(t *testing.T) {
	in := &runnerArgs{
		ToolName:      "render",
		StatusPipe:    "/tmp/w/status.pipe",
		ResultFile:    "/tmp/w/result.json",
		OutputDir:     "/tmp/w/output",
		IPCSocket:     "/tmp/w/ipc.sock",
		ArtifactPaths: map[string]string{"f": "/tmp/w/input/f"},
		VolumeMounts:  []volumeMount{{VolumeID: "v"}},
		UserID:        "u1",
	}
	data, err := marshalIsolatedArgs(in)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out["result_file"] != "/work/result.json" || out["output_dir"] != "/work/output" || out["status_pipe"] != "/work/status.pipe" {
		t.Errorf("paths = %v", out)
	}
	if _, present := out["ipc_socket"]; present {
		t.Errorf("ipc_socket must be empty, got %v", out["ipc_socket"])
	}
	if paths := out["artifact_paths"].(map[string]any); len(paths) != 0 {
		t.Errorf("artifact_paths = %v", paths)
	}
	if out["user_id"] != "u1" || out["tool_name"] != "render" {
		t.Errorf("unrelated fields changed: %v", out)
	}
	if in.ResultFile != "/tmp/w/result.json" {
		t.Error("the caller's args were mutated")
	}
}

func TestIsolatedToolURL(t *testing.T) {
	root := t.TempDir()
	url, err := isolatedToolURL("http://str:8090", root, filepath.Join(root, "my pkg", "render"))
	if err != nil {
		t.Fatal(err)
	}
	if url != "http://str:8090/isolated/tools/my%20pkg/render" {
		t.Errorf("url = %q", url)
	}
	for _, outside := range []string{filepath.Join(root, "..", "elsewhere", "render"), root, "/usr/bin/true"} {
		if _, err := isolatedToolURL("http://str:8090", root, outside); !errors.Is(err, errIsolatedExecutableOutsideToolsets) {
			t.Errorf("isolatedToolURL(%q) error = %v, want errIsolatedExecutableOutsideToolsets", outside, err)
		}
	}
}

func TestIsolatedLogParser_ExtractsResultStatusAndTail(t *testing.T) {
	log := strings.Join([]string{
		"tool starting",
		`@@SAM_STATUS {"status":"step 1"}`,
		"warning: something on stderr",
		`@@SAM_STATUS {"status":"step 2"}`,
		"@@SAM_OUTPUT_FILES 2",
		"@@SAM_RESULT_BEGIN",
		`{"result": {"ok": true,`,
		`  "n": 1}, "error": ""}`,
		"",
		"@@SAM_RESULT_END",
		"",
	}, "\n")

	var statuses []string
	ended := 0
	p := newIsolatedLogParser(func(s string) { statuses = append(statuses, s) }, func() { ended++ }, slog.Default())
	p.consume(strings.NewReader(log))

	if !p.sawEnd || ended != 1 {
		t.Errorf("sawEnd = %v, ended = %d", p.sawEnd, ended)
	}
	if strings.Join(statuses, "|") != "step 1|step 2" {
		t.Errorf("statuses = %v", statuses)
	}
	if p.outputFiles != 2 {
		t.Errorf("outputFiles = %d", p.outputFiles)
	}
	var result resultJSON
	if err := json.Unmarshal(p.resultBytes(), &result); err != nil {
		t.Fatalf("result is not the tool's JSON: %v\n%s", err, p.resultBytes())
	}
	if m, _ := result.Result.(map[string]any); m["ok"] != true {
		t.Errorf("result = %v", result.Result)
	}
	if tail := p.tail.String(); !strings.Contains(tail, "warning: something on stderr") || strings.Contains(tail, "SAM_") || strings.Contains(tail, `"ok"`) {
		t.Errorf("tail = %q", tail)
	}
}

func TestIsolatedLogParser_NoResultSection(t *testing.T) {
	p := newIsolatedLogParser(nil, nil, slog.Default())
	p.consume(strings.NewReader("curl: (22) The requested URL returned error: 404\n"))
	if p.sawEnd || len(p.resultBytes()) != 0 {
		t.Errorf("sawEnd = %v result = %q", p.sawEnd, p.resultBytes())
	}
	if !strings.Contains(p.tail.String(), "404") {
		t.Errorf("tail = %q", p.tail.String())
	}
}

func TestIsolatedLogParser_CapsWhatItKeeps(t *testing.T) {
	t.Run("a result past the cap is dropped and reported", func(t *testing.T) {
		line := strings.Repeat("x", 1<<20)
		var log strings.Builder
		log.WriteString(isolatedResultBegin + "\n")
		for log.Len() <= isolatedMaxResultBytes {
			log.WriteString(line + "\n")
		}
		log.WriteString(isolatedResultEnd + "\n")

		p := newIsolatedLogParser(nil, nil, slog.Default())
		p.consume(strings.NewReader(log.String()))
		if !p.resultTooLarge || !p.sawEnd {
			t.Fatalf("resultTooLarge = %v, sawEnd = %v", p.resultTooLarge, p.sawEnd)
		}
		if got := len(p.resultBytes()); got != 0 {
			t.Errorf("kept %d bytes of a result that is too large", got)
		}
	})

	t.Run("one endless result line is dropped and reported", func(t *testing.T) {
		log := isolatedResultBegin + "\n" + strings.Repeat("x", isolatedMaxLogLineBytes+1) + "\n" + isolatedResultEnd + "\n"
		p := newIsolatedLogParser(nil, nil, slog.Default())
		p.consume(strings.NewReader(log))
		if !p.resultTooLarge || !p.sawEnd {
			t.Fatalf("resultTooLarge = %v, sawEnd = %v", p.resultTooLarge, p.sawEnd)
		}
	})

	t.Run("an endless line outside the result does not spoil it", func(t *testing.T) {
		log := strings.Repeat("x", isolatedMaxLogLineBytes+1) + "\n" + fakePodLog
		p := newIsolatedLogParser(nil, nil, slog.Default())
		p.consume(strings.NewReader(log))
		if p.resultTooLarge || !strings.Contains(string(p.resultBytes()), `"ok": true`) {
			t.Errorf("resultTooLarge = %v, result = %q", p.resultTooLarge, p.resultBytes())
		}
	})
}

func TestWithOwnArch(t *testing.T) {
	pinned := withOwnArch(map[string]string{"pool": "tools"})
	if pinned[kubeArchLabel] != runtimeGOARCH() || pinned["pool"] != "tools" {
		t.Errorf("selector = %v, want the STR's architecture added", pinned)
	}
	chosen := withOwnArch(map[string]string{kubeArchLabel: "amd64"})
	if chosen[kubeArchLabel] != "amd64" {
		t.Errorf("selector = %v, want the template's own choice kept", chosen)
	}
}

func TestJobOutcomeAndFailureDescription(t *testing.T) {
	var job kubeJob
	if _, _, finished := jobOutcome(&job); finished {
		t.Fatal("empty job reported finished")
	}
	job.Status.Conditions = []kubeJobCondition{{Type: "Failed", Status: "True", Reason: "DeadlineExceeded", Message: "Job was active longer than specified deadline"}}
	succeeded, cond, finished := jobOutcome(&job)
	if succeeded || !finished || cond == nil {
		t.Fatalf("succeeded=%v finished=%v cond=%v", succeeded, finished, cond)
	}
	parser := newIsolatedLogParser(nil, nil, slog.Default())
	failure := describeJobFailure(cond, nil, parser)
	if !failure.Deadline || !isolatedTimedOut(failure) {
		t.Errorf("failure = %+v", failure)
	}

	job.Status.Conditions = []kubeJobCondition{{Type: "Complete", Status: "True"}}
	if succeeded, _, finished := jobOutcome(&job); !succeeded || !finished {
		t.Error("Complete condition not recognised")
	}
}

func TestIsolatedToolsHandler_ServesAndRejects(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "render"), []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	secretDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(secretDir, "secret"), []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secretDir, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	h := &unauthenticatedIsolatedToolsHandler{toolsetsDir: root, logger: slog.Default()}

	get := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec
	}
	if rec := get(http.MethodGet, IsolatedToolsURLPath+"pkg/render"); rec.Code != http.StatusOK || rec.Body.String() != "binary" {
		t.Errorf("file: %d %q", rec.Code, rec.Body.String())
	}
	cases := map[string]int{
		IsolatedToolsURLPath + "pkg":                  http.StatusNotFound,
		IsolatedToolsURLPath + "pkg/missing":          http.StatusNotFound,
		IsolatedToolsURLPath + "../etc/passwd":        http.StatusBadRequest,
		IsolatedToolsURLPath + "pkg/../../etc/passwd": http.StatusBadRequest,
		IsolatedToolsURLPath + "escape/secret":        http.StatusNotFound,
		IsolatedToolsURLPath:                          http.StatusBadRequest,
		IsolatedToolsURLPath + "/pkg/render":          http.StatusBadRequest,
	}
	for path, want := range cases {
		if rec := get(http.MethodGet, path); rec.Code != want {
			t.Errorf("GET %s = %d, want %d", path, rec.Code, want)
		}
	}
	if rec := get(http.MethodPost, IsolatedToolsURLPath+"pkg/render"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d", rec.Code)
	}
}

func TestParseIsolatedConfig_TolerationsFromTheConfigFile(t *testing.T) {
	sandbox := config.FromMap(map[string]any{
		"mode": sandboxModeKubernetesJob,
		"job_template": map[string]any{
			"namespace":   "tools-ns",
			"tolerations": []any{map[string]any{"key": "tools", "operator": "Exists", "effect": "NoSchedule"}},
		},
	})
	cfg, err := parseIsolatedConfig(sandbox)
	if err != nil {
		t.Fatal(err)
	}
	want := []IsolatedToleration{{Key: "tools", Operator: "Exists", Effect: "NoSchedule"}}
	if len(cfg.Tolerations) != 1 || cfg.Tolerations[0] != want[0] {
		t.Errorf("tolerations = %+v, want %+v", cfg.Tolerations, want)
	}
}

func TestParseIsolatedConfig(t *testing.T) {
	sandbox := config.FromMap(map[string]any{
		"mode": sandboxModeKubernetesJob,
		"job_template": map[string]any{
			"namespace":     "tools-ns",
			"image":         "str:1",
			"package_url":   "http://str:8090/",
			"runtime_class": "gvisor",
			"node_selector": "pool=tools, tier=a",
			"tolerations":   `[{"key":"tools","operator":"Exists"}]`,
			"memory":        "6Gi",

			"start_timeout_seconds": 45,
		},
	})
	cfg, err := parseIsolatedConfig(sandbox)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Namespace != "tools-ns" || cfg.PackageURL != "http://str:8090" || cfg.RuntimeClass != "gvisor" {
		t.Errorf("cfg = %+v", cfg)
	}
	if cfg.NodeSelector["pool"] != "tools" || cfg.NodeSelector["tier"] != "a" || len(cfg.Tolerations) != 1 {
		t.Errorf("selector/tolerations = %v / %v", cfg.NodeSelector, cfg.Tolerations)
	}
	if cfg.Resources.Memory != "6Gi" || cfg.Resources.CPU != "" {
		t.Errorf("resources = %+v, want memory 6Gi and cpu left to the default", cfg.Resources)
	}
	if cfg.startTimeout() != 45*time.Second {
		t.Errorf("startTimeout = %s, want 45s", cfg.startTimeout())
	}
	if got := (IsolatedConfig{}).startTimeout(); got != defaultIsolatedStartTimeout {
		t.Errorf("unset startTimeout = %s, want the default %s", got, defaultIsolatedStartTimeout)
	}

	if absent, absentErr := parseIsolatedConfig(config.FromMap(map[string]any{})); absentErr != nil || absent.Namespace != "" {
		t.Errorf("absent block: %+v, %v", absent, absentErr)
	}
	for name, template := range map[string]map[string]any{
		"malformed node_selector": {"node_selector": "oops"},
		"malformed tolerations":   {"tolerations": "not json"},
		"bad quantity":            {"memory": "lots"},
		"negative start timeout":  {"start_timeout_seconds": -1},
	} {
		if _, badErr := parseIsolatedConfig(config.FromMap(map[string]any{"job_template": template})); badErr == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestIsolatedConfigValidate(t *testing.T) {
	ok := IsolatedConfig{Namespace: "ns", Image: "i", PackageURL: "http://x", ToolsetsDir: "/t"}
	if err := ok.validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	for name, mutate := range map[string]func(*IsolatedConfig){
		"namespace": func(c *IsolatedConfig) { c.Namespace = "" },
		"url":       func(c *IsolatedConfig) { c.PackageURL = "" },
		"toolsets":  func(c *IsolatedConfig) { c.ToolsetsDir = "" },
	} {
		cfg := ok
		mutate(&cfg)
		if err := cfg.validate(); err == nil {
			t.Errorf("config without %s accepted", name)
		}
	}
}

func TestInit_KubernetesJobModeFailsWithoutABackend(t *testing.T) {
	brk := newTestBroker(t)
	w := NewWorker(&testServices{brk: brk}, WorkerConfig{
		WorkerID:  "runner",
		Namespace: "test-ns",
		Sandbox:   SandboxConfig{Mode: sandboxModeKubernetesJob},
	})
	if err := w.Init(t.Context(), nil); err == nil {
		t.Fatal("Init succeeded in kubernetes-job mode with no job template, so tools would run in the STR pod")
	}
}
