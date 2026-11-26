// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import (
	"context"
	"errors"
	"fmt"
)

const maxSaveNextAttempts = 10

func SaveNextVersion(
	ctx context.Context,
	nextVersion func(context.Context) (int, error),
	saveVersion func(context.Context, int) error,
) (int, error) {
	for attempt := 1; ; attempt++ {
		version, err := nextVersion(ctx)
		if err != nil {
			return 0, fmt.Errorf("find next version: %w", err)
		}
		err = saveVersion(ctx, version)
		if err == nil {
			return version, nil
		}
		if !errors.Is(err, ErrVersionConflict) {
			return 0, err
		}
		if attempt == maxSaveNextAttempts {
			return 0, fmt.Errorf("save next version after %d attempts: %w", attempt, err)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, ctxErr
		}
	}
}

type versionSaver interface {
	SaveVersion(ctx context.Context, loc Locator, version int, content []byte, mimeType string) error
}

func SaveAtNextVersion(ctx context.Context, store versionSaver, loc Locator, content []byte, mimeType string, nextVersion func(context.Context) (int, error)) (int, error) {
	return SaveNextVersion(ctx, nextVersion, func(ctx context.Context, version int) error {
		return store.SaveVersion(ctx, loc, version, content, mimeType)
	})
}
