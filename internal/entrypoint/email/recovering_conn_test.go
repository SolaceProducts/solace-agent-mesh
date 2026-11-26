// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"
)

type panickingConn struct {
	writePanic any
	readPanic  any

	successWrite int
	successRead  []byte
}

func (c *panickingConn) Write(p []byte) (int, error) {
	if c.writePanic != nil {
		panic(c.writePanic)
	}
	if c.successWrite > 0 {
		return c.successWrite, nil
	}
	return len(p), nil
}

func (c *panickingConn) Read(p []byte) (int, error) {
	if c.readPanic != nil {
		panic(c.readPanic)
	}
	if len(c.successRead) > 0 {
		n := copy(p, c.successRead)
		c.successRead = c.successRead[n:]
		return n, nil
	}
	return 0, io.EOF
}

func (c *panickingConn) Close() error                       { return nil }
func (c *panickingConn) LocalAddr() net.Addr                { return nil }
func (c *panickingConn) RemoteAddr() net.Addr               { return nil }
func (c *panickingConn) SetDeadline(_ time.Time) error      { return nil }
func (c *panickingConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *panickingConn) SetWriteDeadline(_ time.Time) error { return nil }

func TestRecoveringConn_WritePanicConvertedToError(t *testing.T) {
	t.Parallel()

	inner := &panickingConn{writePanic: "invalid return from write: got 22 from a write of 13"}
	wrapped := &recoveringConn{Conn: inner, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	n, err := wrapped.Write([]byte("DONE\r\n"))
	if err == nil {
		t.Fatal("expected an error from the recovered panic, got nil")
	}
	if n != 0 {
		t.Errorf("Write returned n=%d on panic recovery; want 0", n)
	}
	if !strings.Contains(err.Error(), "invalid return from write") {
		t.Errorf("error %q does not surface the original panic message", err.Error())
	}
}

func TestRecoveringConn_WriteRuntimePanicConvertedToError(t *testing.T) {
	t.Parallel()

	inner := &panickingConn{writePanic: errors.New("synthetic runtime panic")}
	wrapped := &recoveringConn{Conn: inner, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	_, err := wrapped.Write([]byte("hello"))
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "synthetic runtime panic") {
		t.Errorf("error %q does not surface the original panic message", err.Error())
	}
}

func TestRecoveringConn_WriteHappyPath(t *testing.T) {
	t.Parallel()

	inner := &panickingConn{}
	wrapped := &recoveringConn{Conn: inner, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	body := []byte("hello world")
	n, err := wrapped.Write(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(body) {
		t.Errorf("Write returned n=%d, want %d", n, len(body))
	}
}

func TestRecoveringConn_ReadPanicConvertedToError(t *testing.T) {
	t.Parallel()

	inner := &panickingConn{readPanic: "EOF panic"}
	wrapped := &recoveringConn{Conn: inner, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	buf := make([]byte, 16)
	n, err := wrapped.Read(buf)
	if err == nil {
		t.Fatal("expected an error from the recovered panic, got nil")
	}
	if n != 0 {
		t.Errorf("Read returned n=%d on panic recovery; want 0", n)
	}
	if !strings.Contains(err.Error(), "EOF panic") {
		t.Errorf("error %q does not surface the original panic message", err.Error())
	}
}

func TestRecoveringConn_ReadHappyPath(t *testing.T) {
	t.Parallel()

	inner := &panickingConn{successRead: []byte("payload")}
	wrapped := &recoveringConn{Conn: inner, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	buf := make([]byte, 16)
	n, err := wrapped.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(buf[:n]) != "payload" {
		t.Errorf("Read returned %q, want %q", string(buf[:n]), "payload")
	}
}
