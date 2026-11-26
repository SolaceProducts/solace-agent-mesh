// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillruntime

import "errors"

var (
	ErrSkillNotFound      = errors.New("skill not found")
	ErrInvalidPath        = errors.New("invalid path")
	ErrPathOutsideSkill   = errors.New("path resolves outside skill directory")
	ErrPathNotInResources = errors.New("path must be within references/ or assets/")
	ErrNotAFile           = errors.New("path is not a regular file")
	ErrInvalidPattern     = errors.New("invalid regex pattern")
	ErrInvalidParam       = errors.New("invalid parameter")
)
