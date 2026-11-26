// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

package samtoolsdk

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
)

type statusWriter struct {
	pipePath string
	mu       sync.Mutex
	file     *os.File
}

type statusMessage struct {
	Status string `json:"status"`
}

func newStatusWriter(pipePath string) *statusWriter {
	return &statusWriter{pipePath: pipePath}
}

func (sw *statusWriter) Send(message string) error {
	if sw.pipePath == "" {
		return nil
	}

	sw.mu.Lock()
	defer sw.mu.Unlock()

	if sw.file == nil {
		f, err := sw.openPipeWithRetry()
		if err != nil {
			return nil
		}
		sw.file = f
	}

	data, err := json.Marshal(statusMessage{Status: message})
	if err != nil {
		return fmt.Errorf("marshaling status: %w", err)
	}
	data = append(data, '\n')

	_, err = sw.file.Write(data)
	return err
}

func (sw *statusWriter) openPipeWithRetry() (*os.File, error) {
	const maxRetries = 5
	const retryDelay = 50 * time.Millisecond

	for i := range maxRetries {
		fd, err := syscall.Open(sw.pipePath, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			return os.NewFile(uintptr(fd), sw.pipePath), nil
		}

		if err == syscall.ENXIO {
			if i < maxRetries-1 {
				time.Sleep(retryDelay)
				continue
			}
		}

		return nil, fmt.Errorf("opening status pipe: %w", err)
	}

	return nil, fmt.Errorf("status pipe reader not available after %d retries", maxRetries)
}

func (sw *statusWriter) Close() error {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	if sw.file != nil {
		err := sw.file.Close()
		sw.file = nil
		return err
	}
	return nil
}
