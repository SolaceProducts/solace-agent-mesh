// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package control

import (
	"encoding/json"
	"errors"
)

var ErrResourceNotFound = errors.New("resource not found")

var ErrResourceConflict = errors.New("resource conflict")

const InstanceNotRunning = "is not running"

type Response struct {
	Result any    `json:"result,omitempty"`
	Error  *Error `json:"error,omitempty"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	ErrInvalidRequest   = -32600
	ErrMethodNotAllowed = -32601
	ErrNotFound         = -32001
	ErrConflict         = -32002
	ErrAuthDenied       = -32003
	ErrOperationFailed  = -32004
)

func SuccessResponse(result any) *Response {
	return &Response{Result: result}
}

func ErrorResponse(code int, message string) *Response {
	return &Response{Error: &Error{Code: code, Message: message}}
}

func MarshalResponse(resp *Response) ([]byte, error) {
	return json.Marshal(resp)
}

func UnmarshalResponse(data []byte) (*Response, error) {
	var resp Response
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
