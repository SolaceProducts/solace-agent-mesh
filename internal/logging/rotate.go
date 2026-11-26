// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package logging

import (
	"io"
	"os"

	"gopkg.in/natefinch/lumberjack.v2"
)

type writerOptions struct {
	logFile    string
	maxSizeMB  int
	maxBackups int
	maxAgeDays int
	compress   bool
}

func noopCleanup() {
}

func openLogWriter(opts writerOptions) (io.WriteCloser, func(), error) {
	if opts.logFile == "" {
		return nil, noopCleanup, nil
	}
	if opts.maxSizeMB <= 0 {
		f, err := os.OpenFile(opts.logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, noopCleanup, err
		}
		return f, func() { _ = f.Close() }, nil
	}
	probe, err := os.OpenFile(opts.logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, noopCleanup, err
	}
	_ = probe.Close()

	lj := &lumberjack.Logger{
		Filename:   opts.logFile,
		MaxSize:    opts.maxSizeMB,
		MaxBackups: opts.maxBackups,
		MaxAge:     opts.maxAgeDays,
		Compress:   opts.compress,
	}
	return lj, func() { _ = lj.Close() }, nil
}

type RotatingFileOptions struct {
	Path       string
	MaxSizeMB  int
	MaxBackups int
	MaxAgeDays int
	Compress   bool
}

func NewRotatingFile(opts RotatingFileOptions) (io.WriteCloser, func(), error) {
	return openLogWriter(writerOptions{
		logFile:    opts.Path,
		maxSizeMB:  opts.MaxSizeMB,
		maxBackups: opts.MaxBackups,
		maxAgeDays: opts.MaxAgeDays,
		compress:   opts.Compress,
	})
}
