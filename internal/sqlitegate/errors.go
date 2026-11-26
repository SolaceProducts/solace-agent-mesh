// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sqlitegate

import (
	"errors"
	"fmt"
)

var ErrRefused = errors.New("not a single read-only SELECT statement")

type RefusalReason string

const (
	ReasonNoStatement        RefusalReason = "no_statement"
	ReasonMultipleStatements RefusalReason = "multiple_statements"
	ReasonNotReadOnlySelect  RefusalReason = "not_read_only_select"
	ReasonDeniedAction       RefusalReason = "denied_action"
)

type Refusal struct {
	Reason RefusalReason
	Action int32
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("%s: %s", ErrRefused, r.Reason)
}

func (r *Refusal) Unwrap() error { return ErrRefused }

type CompileError struct{ message string }

func (e *CompileError) Error() string { return e.message }

type Stage string

const (
	StageOpen      Stage = "open"
	StageDefine    Stage = "define"
	StageAuthorize Stage = "authorize"
	StageCompile   Stage = "compile"
)

type InternalError struct {
	Stage      Stage
	ResultCode int32
}

func (e *InternalError) Error() string {
	if e.ResultCode == 0 {
		return fmt.Sprintf("the query check failed at stage %q", e.Stage)
	}
	return fmt.Sprintf("the query check failed at stage %q (SQLite result code %d)", e.Stage, e.ResultCode)
}
