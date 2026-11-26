// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package liquidsafe

import (
	"bytes"
	"errors"
	"io"
	"sync/atomic"
	"time"

	"github.com/osteele/liquid"
)

var DefaultRenderDeadline = 5 * time.Second

var ErrRenderDeadlineExceeded = errors.New("liquid render deadline exceeded")

type deadlineWriter struct {
	w        io.Writer
	tripped  atomic.Bool
	deadline atomic.Bool
}

func (d *deadlineWriter) Write(p []byte) (int, error) {
	if d.deadline.Load() {
		d.tripped.Store(true)
		return 0, ErrRenderDeadlineExceeded
	}
	return d.w.Write(p)
}

func RenderStringWithDeadline(engine *liquid.Engine, source string, bindings liquid.Bindings, deadline time.Duration) (out string, err error) {
	var buf bytes.Buffer
	if deadline <= 0 {
		if e := engine.ParseAndFRender(&buf, []byte(source), bindings); e != nil {
			return buf.String(), e
		}
		return buf.String(), nil
	}

	dw := &deadlineWriter{w: &buf}
	timer := time.AfterFunc(deadline, func() { dw.deadline.Store(true) })
	defer timer.Stop()

	defer func() {
		if r := recover(); r != nil {
			if dw.tripped.Load() {
				out = buf.String()
				err = ErrRenderDeadlineExceeded
				return
			}
			panic(r)
		}
	}()

	renderErr := engine.ParseAndFRender(dw, []byte(source), bindings)
	if dw.tripped.Load() {
		return buf.String(), ErrRenderDeadlineExceeded
	}
	if renderErr != nil {
		return buf.String(), renderErr
	}
	return buf.String(), nil
}
