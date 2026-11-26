// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"strings"
	"unicode"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

const filesArg = "files"

const filesArgDescription = "Files to send with the message, such as documents or images the user attached. " +
	"Each file is stored in the conversation, where the agent can read it."

type inputFile struct {
	Name     string `json:"name"`
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

type decodedFile struct {
	name     string
	mimeType string
	content  []byte
}

type fileInputError struct{ reason string }

func (e fileInputError) Error() string { return e.reason }

func refuseFiles(format string, args ...any) error {
	return fileInputError{reason: fmt.Sprintf(format, args...)}
}

func filesArgOption(cfg *Config) mcplib.ToolOption {
	return mcplib.WithArray(filesArg,
		mcplib.Description(filesArgDescription),
		mcplib.MaxItems(cfg.MaxInputFiles),
		mcplib.Items(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "File name, including its extension.",
				},
				"mimeType": map[string]any{
					"type":        "string",
					"description": "Media type of the file, such as text/csv or application/pdf.",
				},
				"data": map[string]any{
					"type":            "string",
					"description":     fmt.Sprintf("File content, base64-encoded. At most %d bytes once decoded.", cfg.MaxInputFileBytes),
					"contentEncoding": "base64",
				},
			},
			"required": []string{"name", "data"},
		}),
	)
}

func decodeInputFiles(request mcplib.CallToolRequest, cfg *Config) ([]decodedFile, error) {
	if _, present := request.GetArguments()[filesArg]; !present {
		return nil, nil
	}
	if !cfg.EnableFileInput {
		return nil, refuseFiles("this entrypoint does not accept files; send the message without %q", filesArg)
	}
	var args struct {
		Files []inputFile `json:"files"`
	}
	if err := request.BindArguments(&args); err != nil {
		return nil, refuseFiles("%q must be an array of objects with name, mimeType and base64 data", filesArg)
	}
	if len(args.Files) > cfg.MaxInputFiles {
		return nil, refuseFiles("too many files: %d sent, at most %d allowed", len(args.Files), cfg.MaxInputFiles)
	}
	files := make([]decodedFile, 0, len(args.Files))
	for i, f := range args.Files {
		decoded, err := decodeInputFile(i, f, cfg.MaxInputFileBytes)
		if err != nil {
			return nil, err
		}
		files = append(files, decoded)
	}
	return files, nil
}

const maxInputFieldBytes = 255

func decodeInputFile(index int, f inputFile, maxBytes int) (decodedFile, error) {
	name, err := inputFileName(index, f.Name)
	if err != nil {
		return decodedFile{}, err
	}
	mimeType, err := inputFileMIMEType(name, f.MIMEType)
	if err != nil {
		return decodedFile{}, err
	}
	if base64.StdEncoding.DecodedLen(len(f.Data)) > maxBytes+2 {
		return decodedFile{}, refuseFiles("file %q is larger than %d bytes", name, maxBytes)
	}
	content, err := base64.StdEncoding.DecodeString(f.Data)
	if err != nil {
		return decodedFile{}, refuseFiles("file %q is not valid base64", name)
	}
	if len(content) > maxBytes {
		return decodedFile{}, refuseFiles("file %q is larger than %d bytes", name, maxBytes)
	}
	if len(content) == 0 {
		return decodedFile{}, refuseFiles("file %q is empty", name)
	}
	return decodedFile{name: name, mimeType: mimeType, content: content}, nil
}

func inputFileName(index int, raw string) (string, error) {
	name := strings.TrimSpace(raw)
	switch {
	case name == "":
		return "", refuseFiles("file %d has no name", index+1)
	case len(name) > maxInputFieldBytes || strings.ContainsFunc(name, unicode.IsControl):
		return "", refuseFiles("file %d name must be at most %d bytes with no control characters", index+1, maxInputFieldBytes)
	case !artifact.IsFilenameSafe(name) || artifact.IsInternalArtifact(name) || isUserScoped(name):
		return "", refuseFiles("file %q is not an allowed name: use a plain file name without a path, %q or a scope prefix", name, "..")
	}
	return name, nil
}

func isUserScoped(name string) bool {
	_, userScoped := artifact.ResolveFilename(name)
	return userScoped
}

func inputFileMIMEType(name, raw string) (string, error) {
	mimeType := strings.TrimSpace(raw)
	if mimeType == "" {
		return "", nil
	}
	if len(mimeType) > maxInputFieldBytes {
		return "", refuseFiles("file %q mimeType must be at most %d bytes", name, maxInputFieldBytes)
	}
	if _, _, err := mime.ParseMediaType(mimeType); err != nil {
		return "", refuseFiles("file %q mimeType is not a valid media type", name)
	}
	return mimeType, nil
}

func (a *Adapter) storeInputFiles(ctx context.Context, files []decodedFile, userID, sessionID string) ([]a2a.Part, error) {
	if len(files) == 0 {
		return nil, nil
	}
	scope := entrypoint.ArtifactScope{UserID: userID, SessionID: sessionID}
	parts := make([]a2a.Part, 0, len(files))
	totalBytes := 0
	for _, f := range files {
		uri, ok := entrypoint.SaveBytesAsArtifact(ctx, a.core.Services().Artifacts(), scope, entrypoint.ArtifactUpload{
			Filename:   f.name,
			MIMEType:   f.mimeType,
			Content:    f.content,
			DeriveMIME: true,
		}, a.logger)
		if !ok {
			return nil, refuseFiles("file %q could not be stored, so the message was not sent", f.name)
		}
		parts = append(parts, a2a.NewFilePart(uri, f.name, f.mimeType))
		totalBytes += len(f.content)
	}
	a.logger.Debug("mcp tool call files stored", "sessionID", sessionID, "fileCount", len(parts), "totalBytes", totalBytes)
	return parts, nil
}
