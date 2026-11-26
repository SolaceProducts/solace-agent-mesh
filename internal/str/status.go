// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !windows

package str

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"syscall"
	"time"
)

func openStatusFifoHoldingWriteRef(path string) (int, error) {
	return syscall.Open(path, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
}

func readStatusPipe(path string, done <-chan struct{}, cb func(string), logger *slog.Logger) {
	fd, err := openStatusFifoHoldingWriteRef(path)
	if err != nil {
		logger.Debug("failed to open status pipe", "path", path, "error", err)
		return
	}
	f := os.NewFile(uintptr(fd), path)
	defer func() { _ = f.Close() }()

	closerDone := make(chan struct{})
	defer close(closerDone)
	go func() {
		select {
		case <-done:
			_ = f.SetReadDeadline(time.Now())
		case <-closerDone:
		}
	}()

	var partial []byte
	emit := func(chunk []byte) {
		partial = append(partial, chunk...)
		for {
			i := bytes.IndexByte(partial, '\n')
			if i < 0 {
				break
			}
			processStatusLine(partial[:i], cb, logger)
			partial = partial[i+1:]
		}
	}

	buf := make([]byte, 4096)

	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			emit(buf[:n])
		}
		if readErr != nil {
			if errors.Is(readErr, os.ErrDeadlineExceeded) {
				break
			}
			if !errors.Is(readErr, io.EOF) && !errors.Is(readErr, os.ErrClosed) {
				logger.Debug("status pipe read error", "error", readErr)
			}
			return
		}
	}

	for {
		n, rerr := syscall.Read(fd, buf)
		if n > 0 {
			emit(buf[:n])
		}
		if n <= 0 || rerr != nil {
			break
		}
	}

	if len(partial) > 0 {
		processStatusLine(partial, cb, logger)
	}
}
