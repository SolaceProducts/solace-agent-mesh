// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"errors"
	"io"
)

var errFileTooLarge = errors.New("file exceeds maximum inbound size")

const maxInboundFileBytes int64 = 100 << 20

type limitedWriter struct {
	w         io.Writer
	remaining int64
}

func newLimitedWriter(w io.Writer, maxBytes int64) *limitedWriter {
	return &limitedWriter{w: w, remaining: maxBytes}
}

func (lw *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) <= lw.remaining {
		n, err := lw.w.Write(p)
		lw.remaining -= int64(n)
		return n, err
	}
	if lw.remaining > 0 {
		_, _ = lw.w.Write(p[:lw.remaining])
		lw.remaining = 0
	}
	return 0, errFileTooLarge
}
