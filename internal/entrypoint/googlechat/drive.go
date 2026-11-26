// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"

	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
)

const driveFileScope = "https://www.googleapis.com/auth/drive.file"

const defaultDriveAPIBaseURL = "https://www.googleapis.com/drive/v3/"

const AppPropertyTaskID = "samTaskId"

type driveAPI interface {
	Upload(ctx context.Context, folderID, name, mimeType string, content []byte, properties map[string]string) (fileID string, err error)
	ShareWithUser(ctx context.Context, fileID, email string, expires time.Time) error
	Delete(ctx context.Context, fileID string) (trashed bool, err error)
}

type restDriveAPI struct {
	svc *drive.Service
}

func newRestDriveAPI(ctx context.Context, cfg *Config) (*restDriveAPI, error) {
	opts, err := googleClientOptions(ctx, cfg, cfg.DriveAPIBaseURL, driveFileScope)
	if err != nil {
		return nil, err
	}
	svc, err := drive.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("google_chat adapter: build drive client: %w", err)
	}
	return &restDriveAPI{svc: svc}, nil
}

func (c *restDriveAPI) Upload(ctx context.Context, folderID, name, mimeType string, content []byte, properties map[string]string) (fileID string, err error) {
	defer observability.NewGatewayOutboundMonitor(outboundMonitorName, "drive_upload").Stop(&err)
	file := &drive.File{Name: name, Parents: []string{folderID}, MimeType: mimeType, AppProperties: properties}
	created, err := c.svc.Files.Create(file).
		Media(bytes.NewReader(content), googleapi.ContentType(mimeType)).
		SupportsAllDrives(true).
		Fields("id").
		Context(ctx).Do()
	if err != nil {
		return "", err
	}
	return created.Id, nil
}

func (c *restDriveAPI) ShareWithUser(ctx context.Context, fileID, email string, expires time.Time) (err error) {
	defer observability.NewGatewayOutboundMonitor(outboundMonitorName, "drive_share").Stop(&err)
	permission := &drive.Permission{
		Type:           "user",
		Role:           "reader",
		EmailAddress:   email,
		ExpirationTime: expires.UTC().Format(time.RFC3339),
	}
	_, err = c.svc.Permissions.Create(fileID, permission).
		SupportsAllDrives(true).
		SendNotificationEmail(false).
		Fields("id").
		Context(ctx).Do()
	return err
}

func (c *restDriveAPI) Delete(ctx context.Context, fileID string) (trashed bool, err error) {
	defer observability.NewGatewayOutboundMonitor(outboundMonitorName, "drive_delete").Stop(&err)
	deleteErr := c.svc.Files.Delete(fileID).SupportsAllDrives(true).Context(ctx).Do()
	apiErr, ok := errors.AsType[*googleapi.Error](deleteErr)
	if !ok || !refusedForPermission(apiErr) {
		return false, deleteErr
	}
	_, trashErr := c.svc.Files.Update(fileID, &drive.File{Trashed: true}).
		SupportsAllDrives(true).
		Fields("id").
		Context(ctx).Do()
	if trashErr != nil {
		err = &trashFailedError{deleteErr: deleteErr, trashErr: trashErr}
		return false, err
	}
	return true, nil
}

type trashFailedError struct {
	deleteErr, trashErr error
}

func (e *trashFailedError) Error() string {
	return "drive removal failed: delete refused, trash failed"
}
func (e *trashFailedError) Unwrap() error { return e.deleteErr }

func refusedForPermission(err *googleapi.Error) bool {
	if err.Code != http.StatusForbidden {
		return false
	}
	for _, item := range err.Errors {
		if slices.Contains(driveRateLimitReasons, item.Reason) {
			return false
		}
	}
	return true
}

var driveRateLimitReasons = []string{"rateLimitExceeded", "userRateLimitExceeded", "dailyLimitExceeded", "sharingRateLimitExceeded"}
