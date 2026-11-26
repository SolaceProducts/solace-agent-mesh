// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package container

import (
	"testing"
)

func TestBuildRunArgs_Basic(t *testing.T) {
	rt := &CLIRuntime{binary: "docker", typ: RuntimeDocker}

	args := rt.buildRunArgs(RunOptions{
		Image: "sam-str:dev",
		Cmd:   []string{"--config", "/etc/str.yaml"},
	})

	if args[0] != "run" {
		t.Errorf("args[0] = %q, want 'run'", args[0])
	}
	if args[1] != "sam-str:dev" {
		t.Errorf("image not in expected position, got %v", args)
	}
}

func TestBuildRunArgs_Detach(t *testing.T) {
	rt := &CLIRuntime{binary: "docker", typ: RuntimeDocker}

	args := rt.buildRunArgs(RunOptions{
		Image:  "sam-str:dev",
		Detach: true,
	})

	found := false
	for _, a := range args {
		if a == "--detach" {
			found = true
		}
	}
	if !found {
		t.Errorf("--detach not found in args: %v", args)
	}
}

func TestBuildRunArgs_NameAndLabels(t *testing.T) {
	rt := &CLIRuntime{binary: "podman", typ: RuntimePodman}

	args := rt.buildRunArgs(RunOptions{
		Image:  "sam-str:1.0",
		Name:   "sam-str-worker",
		Labels: map[string]string{"sam.component": "str"},
	})

	expectInArgs(t, args, "--name", "sam-str-worker")
	expectInArgs(t, args, "--label", "sam.component=str")
}

func TestBuildRunArgs_EnvAndBinds(t *testing.T) {
	rt := &CLIRuntime{binary: "docker", typ: RuntimeDocker}

	args := rt.buildRunArgs(RunOptions{
		Image:   "sam-str:dev",
		Env:     []string{"FOO=bar", "BAZ=qux"},
		Binds:   []string{"/host/vol:/volumes:ro"},
		Network: "host",
	})

	expectInArgs(t, args, "--env", "FOO=bar")
	expectInArgs(t, args, "--env", "BAZ=qux")
	expectInArgs(t, args, "-v", "/host/vol:/volumes:ro")
	expectInArgs(t, args, "--network", "host")
}

func TestParseContainerList_DockerJSON(t *testing.T) {
	data := `[{"Id":"abc123def456","Names":"/sam-str-worker","Image":"sam-str:dev","State":"running","Status":"Up 5 minutes"}]`

	containers, err := parseContainerList([]byte(data), RuntimeDocker)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(containers) != 1 {
		t.Fatalf("got %d containers, want 1", len(containers))
	}
	if containers[0].ID != "abc123def456" {
		t.Errorf("ID = %q", containers[0].ID)
	}
	if containers[0].Name != "sam-str-worker" {
		t.Errorf("Name = %q, want 'sam-str-worker'", containers[0].Name)
	}
	if containers[0].State != "running" {
		t.Errorf("State = %q", containers[0].State)
	}
}

func TestParseContainerList_PodmanNDJSON(t *testing.T) {
	data := `{"Id":"abc123","Names":["sam-str-worker"],"Image":"sam-str:dev","State":"running","Status":"Up 5 minutes"}
{"Id":"def456","Names":["other"],"Image":"other:latest","State":"exited","Status":"Exited (0) 1 hour ago"}`

	containers, err := parseContainerList([]byte(data), RuntimePodman)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(containers) != 2 {
		t.Fatalf("got %d containers, want 2", len(containers))
	}
	if containers[0].Name != "sam-str-worker" {
		t.Errorf("container[0].Name = %q", containers[0].Name)
	}
	if containers[1].Name != "other" {
		t.Errorf("container[1].Name = %q", containers[1].Name)
	}
}

func TestParseContainerList_Empty(t *testing.T) {
	containers, err := parseContainerList([]byte(""), RuntimeDocker)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(containers) != 0 {
		t.Errorf("got %d containers, want 0", len(containers))
	}
}

func TestContainerJSON_Name_String(t *testing.T) {
	c := containerJSON{Names: "/my-container"}
	if got := c.name(); got != "my-container" {
		t.Errorf("name() = %q, want 'my-container'", got)
	}
}

func TestContainerJSON_Name_Slice(t *testing.T) {
	c := containerJSON{Names: []any{"my-container"}}
	if got := c.name(); got != "my-container" {
		t.Errorf("name() = %q, want 'my-container'", got)
	}
}

func TestContainerJSON_Name_Empty(t *testing.T) {
	c := containerJSON{Names: nil}
	if got := c.name(); got != "" {
		t.Errorf("name() = %q, want ''", got)
	}
}

func expectInArgs(t *testing.T, args []string, flag, value string) {
	t.Helper()
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == value {
			return
		}
	}
	t.Errorf("expected %s %s in args: %v", flag, value, args)
}
