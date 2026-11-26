// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"syscall"
	"testing"
)

func wrappedError(message string) *Result {
	return ErrorFromCaller(message)
}

func TestErrorSite(t *testing.T) {
	const file = "internal/tool/errorsite_test.go:"
	for _, tc := range []struct {
		name string
		got  *Result
	}{
		{"Error records its caller", Error("m")},
		{"Failed records its caller", Failed("m", nil)},
		{"a wrapper records its own caller", wrappedError("m")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.HasPrefix(tc.got.ErrorSite, file) {
				t.Errorf("site = %q, want prefix %q", tc.got.ErrorSite, file)
			}
		})
	}
}

func TestErrorSiteNeverReachesTheWire(t *testing.T) {
	r := Error("m", WithCause(context.DeadlineExceeded))
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal of an error result with a site and class: %v", err)
	}
	for _, leaked := range []string{"errorsite_test.go", "deadline", "ErrorSite", "ErrorClass"} {
		if strings.Contains(string(raw), leaked) {
			t.Errorf("wire form carries %q: %s", leaked, raw)
		}
	}
}

func TestRepoSite(t *testing.T) {
	for _, tc := range []struct {
		name, fn, file, want string
	}{
		{"strips a local checkout prefix", modulePathPrefix + "internal/str.(*Worker).run", "/Users/x/repo/internal/str/worker.go", "internal/str/worker.go:7"},
		{"strips a trimpath prefix", modulePathPrefix + "pkg/tools.Run.func1", modulePathPrefix + "pkg/tools/q.go", "pkg/tools/q.go:7"},
		{"a command package", modulePathPrefix + "cmd/str.run", "/home/ci/work/cmd/str/main.go", "cmd/str/main.go:7"},
		{"code outside the module has no site", "github.com/other/lib.Do", "/go/pkg/mod/lib.go", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RepoSite(tc.fn, tc.file, 7); got != tc.want {
				t.Errorf("RepoSite = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClassifyCause(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"deadline", fmt.Errorf("wrapped: %w", context.DeadlineExceeded), ErrorClassDeadline},
		{"canceled", context.Canceled, ErrorClassCanceled},
		{"errno", fmt.Errorf("x: %w", syscall.ENOSPC), "enospc"},
		{"unknown errno", syscall.Errno(250), "errno_250"},
		{"path error keeps the op and errno, not the path", &fs.PathError{Op: "open", Path: "/home/alice/secret.txt", Err: syscall.ENOENT}, "path_open_enoent"},
		{"path error without an errno", &fs.PathError{Op: "read", Path: "/home/alice/secret.txt", Err: fs.ErrClosed}, "path_read"},
		{"anything else is only other", fmt.Errorf("alice@example.com is not allowed"), ErrorClassOther},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyCause(tc.err); got != tc.want {
				t.Errorf("ClassifyCause = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWithCauseSetsOnlyTheClass(t *testing.T) {
	r := Error("m", WithCause(fmt.Errorf("alice@example.com: %w", context.DeadlineExceeded)))
	if r.ErrorClass != ErrorClassDeadline {
		t.Errorf("class = %q, want %q", r.ErrorClass, ErrorClassDeadline)
	}
}
