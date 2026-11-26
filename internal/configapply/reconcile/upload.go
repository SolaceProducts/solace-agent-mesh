// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

const (
	headerContentType = "Content-Type"
)

const (
	chunkedUploadThreshold = 5 << 20

	uploadAttempts = 3

	uploadInitialBackoff = 250 * time.Millisecond
)

const toolsetUploadsPath = platform.APIV2Prefix + "/toolsetUploads"

type toolsetUploadResponse struct {
	ID        string `json:"id"`
	ChunkSize int64  `json:"chunkSize"`
}

type initUploadRequest struct {
	ToolsetID string `json:"toolsetId"`
	Filename  string `json:"filename"`
	TotalSize int64  `json:"totalSize"`
}

func (c *Client) UploadToolset(ctx context.Context, toolsetID, filename string, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("UploadToolset %s: empty payload", toolsetID)
	}
	if len(data) <= chunkedUploadThreshold {
		return c.uploadToolsetSingle(ctx, toolsetID, filename, data)
	}
	return c.uploadToolsetChunked(ctx, toolsetID, filename, data)
}

func (c *Client) uploadToolsetSingle(ctx context.Context, toolsetID, filename string, data []byte) error {
	method, path := http.MethodPut, toolsetsResource.apiPath()+"/"+url.PathEscape(toolsetID)+"/bundle"
	return retryHTTP(ctx, method+" "+path, func(ctx context.Context) error {
		body, contentType, err := buildMultipart(filename, "application/zip", data)
		if err != nil {
			return err
		}
		req, err := c.newRequest(ctx, method, path, body)
		if err != nil {
			return err
		}
		req.Header.Set(headerContentType, contentType)
		req.Header.Set("Accept", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			return transientErr{err: err}
		}
		defer func() { _ = resp.Body.Close() }()
		respBody, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusServiceUnavailable {
			return transientErr{err: fmt.Errorf("status 503: %s", respBody)}
		}
		if resp.StatusCode >= 400 {
			return &HTTPError{Method: method, Path: path, StatusCode: resp.StatusCode, Body: string(respBody)}
		}
		return nil
	})
}

func (c *Client) uploadToolsetChunked(ctx context.Context, toolsetID, filename string, data []byte) error {
	uploadID, chunkSize, err := c.initToolsetUpload(ctx, toolsetID, filename, int64(len(data)))
	if err != nil {
		return fmt.Errorf("initToolsetUpload: %w", err)
	}
	if chunkSize <= 0 {
		return fmt.Errorf("initToolsetUpload: the platform gave no chunk size")
	}

	for i, offset := 0, int64(0); offset < int64(len(data)); i, offset = i+1, offset+chunkSize {
		end := min(offset+chunkSize, int64(len(data)))
		if err := c.uploadChunk(ctx, uploadID, i, data[offset:end]); err != nil {
			return fmt.Errorf("uploadChunk %d: %w", i, err)
		}
	}

	if err := c.completeToolsetUpload(ctx, uploadID); err != nil {
		return fmt.Errorf("completeToolsetUpload: %w", err)
	}
	return nil
}

func (c *Client) initToolsetUpload(ctx context.Context, toolsetID, filename string, totalSize int64) (string, int64, error) {
	body := initUploadRequest{ToolsetID: toolsetID, Filename: filename, TotalSize: totalSize}
	var resp toolsetUploadResponse
	err := retryHTTP(ctx, "POST /toolsetUploads", func(ctx context.Context) error {
		return transientOn503(c.doSingle(ctx, http.MethodPost, toolsetUploadsPath, body, &resp))
	})
	if err != nil {
		return "", 0, err
	}
	return resp.ID, resp.ChunkSize, nil
}

func (c *Client) uploadChunk(ctx context.Context, uploadID string, chunkNumber int, data []byte) error {
	path := toolsetUploadsPath + "/" + url.PathEscape(uploadID) + "/chunks/" + strconv.Itoa(chunkNumber)
	return retryHTTP(ctx, "PUT "+path, func(ctx context.Context) error {
		req, err := c.newRequest(ctx, "PUT", path, bytes.NewReader(data))
		if err != nil {
			return err
		}
		req.Header.Set(headerContentType, "application/octet-stream")
		resp, err := c.http.Do(req)
		if err != nil {
			return transientErr{err: err}
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusServiceUnavailable {
			return transientErr{err: fmt.Errorf("status 503: %s", body)}
		}
		if resp.StatusCode >= 400 {
			return &HTTPError{Method: "PUT", Path: path, StatusCode: resp.StatusCode, Body: string(body)}
		}
		return nil
	})
}

func (c *Client) completeToolsetUpload(ctx context.Context, uploadID string) error {
	return c.doSingle(ctx, http.MethodPut, toolsetUploadsPath+"/"+url.PathEscape(uploadID)+"/complete", nil, nil)
}

func transientOn503(err error) error {
	if httpErr, ok := errors.AsType[*HTTPError](err); ok && httpErr.StatusCode == http.StatusServiceUnavailable {
		return transientErr{err: err}
	}
	return err
}

type transientErr struct{ err error }

func (e transientErr) Error() string { return e.err.Error() }
func (e transientErr) Unwrap() error { return e.err }

func retryHTTP(ctx context.Context, label string, fn func(ctx context.Context) error) error {
	backoff := uploadInitialBackoff
	var lastErr error
	for attempt := range uploadAttempts {
		err := fn(ctx)
		if err == nil {
			return nil
		}
		lastErr = err

		var t transientErr
		if !errors.As(err, &t) {
			return err
		}
		if attempt == uploadAttempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: %w", label, ctx.Err())
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return fmt.Errorf("%s: gave up after %d attempts: %w", label, uploadAttempts, lastErr)
}

func buildMultipart(filename, partContentType string, data []byte) (io.Reader, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	header.Set(headerContentType, partContentType)
	part, err := mw.CreatePart(header)
	if err != nil {
		return nil, "", fmt.Errorf("create form part: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return nil, "", fmt.Errorf("write form part: %w", err)
	}
	if err := mw.Close(); err != nil {
		return nil, "", fmt.Errorf("close form: %w", err)
	}
	return &buf, mw.FormDataContentType(), nil
}
