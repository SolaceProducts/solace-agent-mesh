// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

const (
	WireSchema        = "ToolResult"
	WireSchemaVersion = "1.0"
)

type wireResult struct {
	Schema        string           `json:"_schema"`
	SchemaVersion string           `json:"_schema_version"`
	Status        string           `json:"status"`
	Message       string           `json:"message"`
	Data          map[string]any   `json:"data,omitempty"`
	DataObjects   []wireDataObject `json:"data_objects,omitempty"`
	ErrorCode     *string          `json:"error_code"`
}

type wireDataObject struct {
	Name           string         `json:"name"`
	Content        *string        `json:"content"`
	IsBinary       bool           `json:"is_binary"`
	ArtifactRef    *ArtifactRef   `json:"artifact_ref"`
	MIMEType       string         `json:"mime_type"`
	Disposition    string         `json:"disposition"`
	InlineMaxBytes int            `json:"inline_max_bytes,omitempty"`
	Description    *string        `json:"description"`
	Preview        *string        `json:"preview"`
	Metadata       map[string]any `json:"metadata"`
	Tags           []string       `json:"tags,omitempty"`
}

func MarshalWire(r *Result) ([]byte, error) {
	wr := wireResult{
		Schema:        WireSchema,
		SchemaVersion: WireSchemaVersion,
		Status:        r.Status,
		Message:       r.Message,
		Data:          r.Data,
		ErrorCode:     nilIfEmpty(r.ErrorCode),
	}

	for _, do := range r.DataObjects {
		wdo := wireDataObject{
			Name:           do.Name,
			ArtifactRef:    do.ArtifactRef,
			MIMEType:       do.MIMEType,
			Disposition:    string(do.Disposition),
			InlineMaxBytes: do.InlineMaxBytes,
			Description:    nilIfEmpty(do.Description),
			Preview:        nilIfEmpty(do.Preview),
			Metadata:       do.Metadata,
			Tags:           do.Tags,
		}

		if do.ArtifactRef != nil {
			wdo.Content = nil
		} else if do.Content != nil {
			if utf8.Valid(do.Content) {
				s := string(do.Content)
				wdo.Content = &s
				wdo.IsBinary = false
			} else {
				s := base64.StdEncoding.EncodeToString(do.Content)
				wdo.Content = &s
				wdo.IsBinary = true
			}
		}

		wr.DataObjects = append(wr.DataObjects, wdo)
	}

	return json.Marshal(wr)
}

func UnmarshalWire(data []byte) (*Result, error) {
	var probe map[string]any
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("unmarshal wire: %w", err)
	}

	if probe["_schema"] != WireSchema {
		return &Result{
			Status:  StatusSuccess,
			Message: "Tool completed",
			Data:    probe,
		}, nil
	}

	var wr wireResult
	if err := json.Unmarshal(data, &wr); err != nil {
		return nil, fmt.Errorf("unmarshal wire result: %w", err)
	}

	r := &Result{
		Status:  wr.Status,
		Message: wr.Message,
		Data:    wr.Data,
	}
	if wr.ErrorCode != nil {
		r.ErrorCode = *wr.ErrorCode
	}

	for _, wdo := range wr.DataObjects {
		do := DataObject{
			Name:           wdo.Name,
			ArtifactRef:    wdo.ArtifactRef,
			MIMEType:       wdo.MIMEType,
			Disposition:    DataDisposition(wdo.Disposition),
			InlineMaxBytes: wdo.InlineMaxBytes,
		}
		if wdo.Description != nil {
			do.Description = *wdo.Description
		}
		if wdo.Preview != nil {
			do.Preview = *wdo.Preview
		}
		do.Metadata = wdo.Metadata
		do.Tags = wdo.Tags

		if wdo.Content != nil {
			if wdo.IsBinary {
				decoded, err := base64.StdEncoding.DecodeString(*wdo.Content)
				if err != nil {
					return nil, fmt.Errorf("decode binary content for %q: %w", wdo.Name, err)
				}
				do.Content = decoded
			} else {
				do.Content = []byte(*wdo.Content)
			}
		}

		r.DataObjects = append(r.DataObjects, do)
	}

	return r, nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func IsSerializedToolResult(payload map[string]any) bool {
	if payload == nil {
		return false
	}
	return payload["_schema"] == WireSchema
}

func UnmarshalToolResultMap(payload map[string]any) (*Result, bool) {
	if !IsSerializedToolResult(payload) {
		return nil, false
	}

	r := &Result{}
	if s, ok := payload["status"].(string); ok {
		r.Status = s
	}
	if s, ok := payload["message"].(string); ok {
		r.Message = s
	}
	if s, ok := payload["error_code"].(string); ok {
		r.ErrorCode = s
	}
	if d, ok := payload["data"].(map[string]any); ok {
		r.Data = d
	}

	rawObjs, _ := payload["data_objects"].([]any)
	for _, raw := range rawObjs {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		do, err := dataObjectFromMap(m)
		if err != nil {
			continue
		}
		r.DataObjects = append(r.DataObjects, do)
	}

	return r, true
}

func dataObjectFromMap(m map[string]any) (DataObject, error) {
	do := DataObject{}
	if s, ok := m["name"].(string); ok {
		do.Name = s
	}
	if s, ok := m["mime_type"].(string); ok {
		do.MIMEType = s
	}
	if s, ok := m["disposition"].(string); ok {
		do.Disposition = DataDisposition(s)
	}
	if v, ok := m["inline_max_bytes"].(float64); ok {
		do.InlineMaxBytes = int(v)
	}
	if s, ok := m["description"].(string); ok {
		do.Description = s
	}
	if s, ok := m["preview"].(string); ok {
		do.Preview = s
	}
	if meta, ok := m["metadata"].(map[string]any); ok {
		do.Metadata = meta
	}
	if rawTags, ok := m["tags"].([]any); ok {
		for _, t := range rawTags {
			if s, ok := t.(string); ok && s != "" {
				do.Tags = append(do.Tags, s)
			}
		}
	}

	if refMap, ok := m["artifact_ref"].(map[string]any); ok {
		ref := &ArtifactRef{}
		if s, ok := refMap["filename"].(string); ok {
			ref.Filename = s
		}
		if v, ok := refMap["version"].(float64); ok {
			ref.Version = int(v)
		}
		if v, ok := refMap["size_bytes"].(float64); ok {
			ref.SizeBytes = int64(v)
		}
		do.ArtifactRef = ref
	}

	if contentRaw, ok := m["content"]; ok && contentRaw != nil {
		s, isStr := contentRaw.(string)
		if !isStr {
			return do, fmt.Errorf("content field is %T, expected string or null", contentRaw)
		}
		isBinary, _ := m["is_binary"].(bool)
		if isBinary {
			decoded, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				return do, fmt.Errorf("decode binary content for %q: %w", do.Name, err)
			}
			do.Content = decoded
		} else {
			do.Content = []byte(s)
		}
	}

	return do, nil
}
