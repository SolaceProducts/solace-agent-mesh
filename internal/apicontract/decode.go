// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apicontract

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

func DecodeJSON(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, MaxJSONBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("request body is empty")
		}
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return bodyTooLargeError{cause: err}
		}
		return err
	}
	return nil
}

type bodyTooLargeError struct{ cause error }

func (bodyTooLargeError) Error() string {
	return fmt.Sprintf("request body exceeds maximum size of %d MB", MaxJSONBodyBytes>>20)
}

func (e bodyTooLargeError) Unwrap() error { return e.cause }

const MaxJSONBodyBytes = 16 << 20
