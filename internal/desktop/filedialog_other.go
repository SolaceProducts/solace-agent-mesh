// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build desktop && !darwin

package desktop

import (
	"context"
	"fmt"
	"runtime"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func nativeOpenFileDialog(ctx context.Context, title string) (string, error) {
	return wailsruntime.OpenFileDialog(ctx, wailsruntime.OpenDialogOptions{
		Title: title,
	})
}

func nativeSaveFileDialog(ctx context.Context, title, defaultName, defaultDir string) (string, error) {
	opts := wailsruntime.SaveDialogOptions{
		Title:           title,
		DefaultFilename: defaultName,
	}
	if runtime.GOOS != "windows" {
		opts.DefaultDirectory = defaultDir
	}
	return wailsruntime.SaveFileDialog(ctx, opts)
}

func nativeOpenFolderDialog(ctx context.Context, title string) (string, error) {
	if title == "" {
		title = "Select Directory"
	}
	path, err := wailsruntime.OpenDirectoryDialog(ctx, wailsruntime.OpenDialogOptions{
		Title: title,
	})
	if err != nil {
		return "", fmt.Errorf("wails open directory dialog: %w", err)
	}
	return path, nil
}
