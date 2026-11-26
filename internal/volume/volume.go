// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package volume

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Service interface {
	Create(ctx context.Context, opts CreateOptions) (string, error)

	Get(ctx context.Context, id string) (*Volume, error)

	List(ctx context.Context, userID string, filter ListFilter) ([]*Volume, error)

	Delete(ctx context.Context, id string, callerType CallerType) error

	RemoveIfExpired(ctx context.Context, id string) (bool, error)

	AcquireWriteLock(ctx context.Context, id string, holder string, maxDuration time.Duration) error

	ReleaseWriteLock(ctx context.Context, id string, holder string) error

	RecordAccess(ctx context.Context, id string) error

	Resolve(ctx context.Context, id string) (string, error)

	CleanupExpired(ctx context.Context) (int, error)

	AssociateSession(ctx context.Context, volumeID string, sessionID string) error

	ListSessionVolumes(ctx context.Context, sessionID string) ([]string, error)

	UserUsage(ctx context.Context, userID string) (int64, error)

	RefreshSize(ctx context.Context, id string) (int64, error)

	RefreshAllSizes(ctx context.Context) (int, error)

	PerUserQuotaBytes() int64
}

type Volume struct {
	ID          string
	Name        string
	Description string
	UserID      string
	CreatorType CallerType
	CreatorName string
	CreatedAt   time.Time
	LastAccess  time.Time
	TTL         *time.Duration
	ExpiresAt   *time.Time
	SizeBytes   int64
	Path        string
	WriteLock   *WriteLock

	Linked                bool
	ExternalPath          string
	ExternalPathContainer string
}

func (v *Volume) IsExpired() bool {
	if v.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*v.ExpiresAt)
}

type WriteLock struct {
	Holder     string
	AcquiredAt time.Time
	MaxExpiry  time.Time
}

func (wl *WriteLock) IsExpired() bool {
	return time.Now().After(wl.MaxExpiry)
}

type CreateOptions struct {
	ID          string
	Name        string
	Description string
	UserID      string
	CreatorType CallerType
	CreatorName string
	TTL         *time.Duration
	MaxSize     *int64

	ExternalPath          string
	ExternalPathContainer string
	CopyFromPath          string
}

type ListFilter struct {
	CreatorType *CallerType
	SessionID   *string
}

type CallerType string

const (
	CallerHuman CallerType = "human"
	CallerAgent CallerType = "agent"
)

type RuntimeView int

const (
	ViewHost RuntimeView = iota
	ViewContainer
)

var (
	ErrNotFound       = errors.New("volume not found")
	ErrLocked         = errors.New("volume is write-locked")
	ErrNotLockHolder  = errors.New("lock not held by this holder")
	ErrPermission     = errors.New("permission denied")
	ErrExpired        = errors.New("volume has expired")
	ErrInvalidOptions = errors.New("invalid create options")
	ErrQuotaExceeded  = errors.New("user storage quota exceeded")
	ErrAlreadyExists  = errors.New("a volume with this id already exists")
)

type LockError struct {
	VolumeID string
	Holder   string
	Until    time.Time
}

func (e *LockError) Error() string {
	return fmt.Sprintf("volume %s is write-locked by %s until %s",
		e.VolumeID, e.Holder, e.Until.Format(time.RFC3339))
}

func (e *LockError) Unwrap() error { return ErrLocked }

type QuotaError struct {
	UserID     string
	UsedBytes  int64
	WouldAdd   int64
	LimitBytes int64
}

func (e *QuotaError) Error() string {
	return fmt.Sprintf("user %s storage quota exceeded: used %d + adding %d > limit %d bytes",
		e.UserID, e.UsedBytes, e.WouldAdd, e.LimitBytes)
}

func (e *QuotaError) Unwrap() error { return ErrQuotaExceeded }
