// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

const modulePathPrefix = "github.com/SolaceProducts/solace-agent-mesh/"

const (
	ErrorClassDeadline = "deadline"
	ErrorClassCanceled = "canceled"
	ErrorClassOther    = "other"
)

func CallerSite(skip int) string {
	var pcs [1]uintptr
	if runtime.Callers(skip+2, pcs[:]) == 0 {
		return ""
	}
	frame, _ := runtime.CallersFrames(pcs[:]).Next()
	return RepoSite(frame.Function, frame.File, frame.Line)
}

func RepoSite(funcName, file string, line int) string {
	rest, ok := strings.CutPrefix(funcName, modulePathPrefix)
	if !ok {
		return ""
	}
	dir := ""
	if i := strings.LastIndex(rest, "/"); i >= 0 {
		dir, rest = rest[:i+1], rest[i+1:]
	}
	pkg, _, _ := strings.Cut(rest, ".")
	if pkg == "" {
		return ""
	}
	return dir + pkg + "/" + filepath.Base(file) + ":" + strconv.Itoa(line)
}

func WithCause(err error) ResultOption {
	return func(r *Result) { r.ErrorClass = ClassifyCause(err) }
}

func ClassifyCause(err error) string {
	if err == nil {
		return ""
	}
	if c := ctxClass(err); c != "" {
		return c
	}
	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		if name := errnoName(pe.Err); name != "" {
			return "path_" + sanitizeOp(pe.Op) + "_" + name
		}
		return "path_" + sanitizeOp(pe.Op)
	}
	if name := errnoName(err); name != "" {
		return name
	}
	return ErrorClassOther
}

func ctxClass(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return ErrorClassDeadline
	case errors.Is(err, context.Canceled):
		return ErrorClassCanceled
	}
	return ""
}

func sanitizeOp(op string) string {
	var b strings.Builder
	for _, r := range op {
		if r >= 'a' && r <= 'z' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 || b.Len() > 16 {
		return "op"
	}
	return b.String()
}

var errnoNames = map[syscall.Errno]string{
	syscall.ENOENT:       "enoent",
	syscall.EACCES:       "eacces",
	syscall.EPERM:        "eperm",
	syscall.ENOSPC:       "enospc",
	syscall.EEXIST:       "eexist",
	syscall.EISDIR:       "eisdir",
	syscall.ENOTDIR:      "enotdir",
	syscall.EINVAL:       "einval",
	syscall.EMFILE:       "emfile",
	syscall.ENOMEM:       "enomem",
	syscall.EPIPE:        "epipe",
	syscall.ECONNREFUSED: "econnrefused",
	syscall.ECONNRESET:   "econnreset",
	syscall.ETIMEDOUT:    "etimedout",
}

func errnoName(err error) string {
	errno, ok := errors.AsType[syscall.Errno](err)
	if !ok {
		return ""
	}
	if name, known := errnoNames[errno]; known {
		return name
	}
	return "errno_" + strconv.Itoa(int(errno))
}
