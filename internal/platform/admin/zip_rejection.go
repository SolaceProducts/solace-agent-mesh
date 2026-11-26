// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import "errors"

var (
	errInvalidZIP         = errors.New("invalid ZIP file")
	errSkillMDUnreadable  = errors.New("SKILL.md could not be read")
	errSkillMDFrontmatter = errors.New("SKILL.md frontmatter is not valid YAML")
)

type readerFailure struct {
	sentence    error
	v1Text      string
	upstreamErr error
}

func newReaderFailure(sentence error, v1Prefix string, err error) error {
	return &readerFailure{sentence: sentence, v1Text: v1Prefix + ": " + err.Error(), upstreamErr: err}
}

func (e *readerFailure) Error() string { return e.v1Text }

func (e *readerFailure) Unwrap() []error { return []error{e.sentence, e.upstreamErr} }

func zipRejection(err error) error {
	if valErr, ok := errors.AsType[*ValidationError](err); ok {
		if valErr.Field == "" && len(valErr.Details) == 0 {
			valErr.Field = fieldPackageFile
		}
		return valErr
	}
	if failure, ok := errors.AsType[*readerFailure](err); ok {
		return validationErrorWith{&ValidationError{Field: fieldPackageFile, Message: failure.sentence.Error()}, err}
	}
	return &ValidationError{Field: fieldPackageFile, Message: err.Error()}
}

func v1ValidationMessage(err error, validErr *ValidationError) string {
	if failure, ok := errors.AsType[*readerFailure](err); ok {
		return failure.v1Text
	}
	return validErr.Message
}
