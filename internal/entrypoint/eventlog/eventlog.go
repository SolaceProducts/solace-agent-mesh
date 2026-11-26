// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventlog

import (
	"context"
	"errors"
)

var ErrTaskNotFound = errors.New("task not found")

var ErrTaskCompleted = errors.New("task already completed")

var ErrHeldForRedelivery = errors.New("event held for redelivery")

var ErrEventRejected = errors.New("event rejected by the database")

type Event struct {
	Sequence int64

	TaskID string

	EventType string

	Payload []byte

	Timestamp int64
}

type TaskEventLog interface {
	Append(ctx context.Context, taskID, sessionID, userID, eventType string, payload []byte) (int64, error)

	Subscribe(ctx context.Context, taskID string, fromSeq int64) (<-chan Event, error)

	Replay(ctx context.Context, taskID string, fromSeq int64) ([]Event, error)

	Complete(ctx context.Context, taskID string) error

	Completed(ctx context.Context, taskID string) (bool, error)

	Cleanup(ctx context.Context, taskID string) error

	Forget(taskID string)

	FindSequenceAfterTimestamp(ctx context.Context, taskID string, epochMs int64) (int64, error)

	FindLastEventByDataType(ctx context.Context, taskID string, dataType string) []byte
}
