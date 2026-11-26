// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package liquidsafe

import (
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRenderStringWithDeadline_PathologicalLoopAborts(t *testing.T) {
	engine := NewEngine()
	const tmpl = `{% for n in (1..10000000) %}{{ n }},{% endfor %}`

	start := time.Now()
	out, err := RenderStringWithDeadline(engine, tmpl, nil, 50*time.Millisecond)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrRenderDeadlineExceeded) {
		t.Fatalf("expected ErrRenderDeadlineExceeded; got err=%v out_bytes=%d elapsed=%s", err, len(out), elapsed)
	}
	if elapsed > 2*time.Second {
		t.Errorf("deadline did not abort promptly: elapsed=%s", elapsed)
	}
}

func TestRenderStringWithDeadline_NormalRender(t *testing.T) {
	engine := NewEngine()
	const tmpl = `{% for n in (1..10) %}{{ n }},{% endfor %}`

	out, err := RenderStringWithDeadline(engine, tmpl, nil, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "1,2,3,4,5,6,7,8,9,10," {
		t.Errorf("unexpected output: %q", out)
	}
}

func TestRenderStringWithDeadline_NoGoroutineLeak(t *testing.T) {
	engine := NewEngine()
	const tmpl = `{% for n in (1..10000000) %}{{ n }},{% endfor %}`

	runtime.GC()
	before := runtime.NumGoroutine()

	_, err := RenderStringWithDeadline(engine, tmpl, nil, 25*time.Millisecond)
	if !errors.Is(err, ErrRenderDeadlineExceeded) {
		t.Fatalf("expected deadline error; got: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > before {
		t.Errorf("goroutine leak after deadline trip: before=%d after=%d", before, got)
	}
}

func TestRenderStringWithDeadline_ZeroDeadlineUncapped(t *testing.T) {
	engine := NewEngine()
	const tmpl = `hello {{ name }}`

	out, err := RenderStringWithDeadline(engine, tmpl, map[string]any{"name": "world"}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "hello world" {
		t.Errorf("unexpected output: %q", out)
	}
}

func TestRenderStringWithDeadline_PartialOutputReturned(t *testing.T) {
	engine := NewEngine()
	const tmpl = `{% for n in (1..10000000) %}{{ n }},{% endfor %}`

	out, err := RenderStringWithDeadline(engine, tmpl, nil, 50*time.Millisecond)
	if !errors.Is(err, ErrRenderDeadlineExceeded) {
		t.Fatalf("expected deadline error; got: %v", err)
	}
	if !strings.HasPrefix(out, "1,") {
		t.Errorf("expected partial output to start with \"1,\"; got %q", out[:min(len(out), 64)])
	}
}
