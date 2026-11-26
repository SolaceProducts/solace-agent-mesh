// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build windows

package str

import (
	"bufio"
	"io"
	"log/slog"
	"os"
	"time"
)

func readStatusPipe(path string, done <-chan struct{}, cb func(string), logger *slog.Logger) {
	f, err := os.Open(path)
	if err != nil {
		logger.Debug("failed to open status file", "path", path, "error", err)
		return
	}
	defer func() { _ = f.Close() }()

	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadBytes('\n')
		processStatusLine(line, cb, logger)
		if err == io.EOF {
			select {
			case <-done:
				drainStatusReader(reader, cb, logger)
				return
			default:
				time.Sleep(100 * time.Millisecond)
				continue
			}
		}
		if err != nil {
			logger.Debug("status file read error", "error", err)
			return
		}
	}
}

func drainStatusReader(reader *bufio.Reader, cb func(string), logger *slog.Logger) {
	for {
		line, err := reader.ReadBytes('\n')
		processStatusLine(line, cb, logger)
		if err != nil {
			return
		}
	}
}
