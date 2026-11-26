// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"bytes"
	"errors"
	"testing"
)

func TestLimitedWriter_UnderCap(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	lw := newLimitedWriter(&buf, 10)

	n, err := lw.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Fatalf("n = %d, want 5", n)
	}
	if buf.String() != "hello" {
		t.Fatalf("buf = %q, want %q", buf.String(), "hello")
	}
}

func TestLimitedWriter_ExactlyCap(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	lw := newLimitedWriter(&buf, 5)

	if _, err := lw.Write([]byte("hello")); err != nil {
		t.Fatalf("writing exactly cap should succeed, got %v", err)
	}
	if buf.Len() != 5 {
		t.Fatalf("buf len = %d, want 5", buf.Len())
	}
}

func TestLimitedWriter_OverCapSingleWrite(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	lw := newLimitedWriter(&buf, 5)

	_, err := lw.Write([]byte("hello world"))
	if !errors.Is(err, errFileTooLarge) {
		t.Fatalf("err = %v, want errFileTooLarge", err)
	}
	if buf.Len() > 5 {
		t.Fatalf("buf len = %d, must not exceed cap 5", buf.Len())
	}
}

func TestLimitedWriter_OverCapManyChunks(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	lw := newLimitedWriter(&buf, 4)

	if _, err := lw.Write([]byte("ab")); err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	if _, err := lw.Write([]byte("cd")); err != nil {
		t.Fatalf("second chunk (fills to cap): %v", err)
	}
	_, err := lw.Write([]byte("ef"))
	if !errors.Is(err, errFileTooLarge) {
		t.Fatalf("third chunk err = %v, want errFileTooLarge", err)
	}
	if buf.Len() > 4 {
		t.Fatalf("buf len = %d, must not exceed cap 4", buf.Len())
	}
}

func TestLimitedWriter_ZeroBytes(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	lw := newLimitedWriter(&buf, 8)

	if _, err := lw.Write(nil); err != nil {
		t.Fatalf("empty write should succeed, got %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("buf len = %d, want 0", buf.Len())
	}
}
