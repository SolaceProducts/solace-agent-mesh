// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package container

import (
	"log/slog"
	"testing"
)

func TestNew_UnsupportedType(t *testing.T) {
	_, err := New("containerd", slog.Default())
	if err == nil {
		t.Fatal("expected error for unsupported runtime type")
	}
}

func TestNew_EmptyTypeFallsToDetect(t *testing.T) {
	rt, err := New("", slog.Default())
	if err != nil {
		t.Logf("no runtime detected: %v", err)
		return
	}
	if rt == nil {
		t.Fatal("expected non-nil runtime")
	}
	typ := rt.Type()
	if typ != RuntimeDocker && typ != RuntimePodman && typ != RuntimeOrbStack {
		t.Errorf("unexpected runtime type: %q", typ)
	}
}

func TestCLIRuntime_Type(t *testing.T) {
	rt := &CLIRuntime{binary: "docker", typ: RuntimeDocker}
	if rt.Type() != RuntimeDocker {
		t.Errorf("Type() = %q, want docker", rt.Type())
	}

	rt2 := &CLIRuntime{binary: "podman", typ: RuntimePodman}
	if rt2.Type() != RuntimePodman {
		t.Errorf("Type() = %q, want podman", rt2.Type())
	}
}

func TestBuildRunArgs_AllOptions(t *testing.T) {
	rt := &CLIRuntime{binary: "docker", typ: RuntimeDocker}
	args := rt.buildRunArgs(RunOptions{
		Image:   "test:latest",
		Name:    "test-container",
		Labels:  map[string]string{"env": "test", "app": "sam"},
		Env:     []string{"A=1", "B=2"},
		Binds:   []string{"/a:/b:ro", "/c:/d"},
		Network: "bridge",
		Cmd:     []string{"--serve", "--port", "8080"},
		Detach:  true,
	})

	expectInArgs(t, args, "--name", "test-container")
	expectInArgs(t, args, "--network", "bridge")
	expectInArgs(t, args, "--env", "A=1")
	expectInArgs(t, args, "--env", "B=2")
	expectInArgs(t, args, "-v", "/a:/b:ro")
	expectInArgs(t, args, "-v", "/c:/d")

	found := false
	for _, a := range args {
		if a == "--detach" {
			found = true
		}
	}
	if !found {
		t.Error("--detach not found")
	}

	last3 := args[len(args)-3:]
	if last3[0] != "--serve" || last3[1] != "--port" || last3[2] != "8080" {
		t.Errorf("cmd args not at end: %v", args)
	}
}

func TestParseContainerList_MalformedJSON(t *testing.T) {
	_, err := parseContainerList([]byte(`{invalid json`), RuntimeDocker)
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestParseContainerList_DockerArray_Multiple(t *testing.T) {
	data := `[
		{"Id":"aaa","Names":"/c1","Image":"img1","State":"running","Status":"Up"},
		{"Id":"bbb","Names":"/c2","Image":"img2","State":"exited","Status":"Exited"}
	]`
	containers, err := parseContainerList([]byte(data), RuntimeDocker)
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 2 {
		t.Fatalf("got %d, want 2", len(containers))
	}
	if containers[0].Image != "img1" {
		t.Errorf("containers[0].Image = %q", containers[0].Image)
	}
	if containers[1].State != "exited" {
		t.Errorf("containers[1].State = %q", containers[1].State)
	}
}

func TestParseContainerList_PodmanNDJSON_SingleLine(t *testing.T) {
	data := `{"Id":"xyz","Names":["single"],"Image":"test:v1","State":"created","Status":"Created"}`
	containers, err := parseContainerList([]byte(data), RuntimePodman)
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 1 {
		t.Fatalf("got %d, want 1", len(containers))
	}
	if containers[0].Name != "single" {
		t.Errorf("name = %q, want single", containers[0].Name)
	}
}

func TestContainerJSON_Name_SlashStripping(t *testing.T) {
	c := containerJSON{Names: "/my-container"}
	if got := c.name(); got != "my-container" {
		t.Errorf("name() = %q, want 'my-container'", got)
	}
}

func TestContainerJSON_Name_EmptySlice(t *testing.T) {
	c := containerJSON{Names: []any{}}
	if got := c.name(); got != "" {
		t.Errorf("name() = %q, want empty", got)
	}
}
