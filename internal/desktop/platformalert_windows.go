// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build desktop

package desktop

import "golang.org/x/sys/windows"

func showPlatformAlert(title, message string) {
	titlePtr, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	messagePtr, err := windows.UTF16PtrFromString(message)
	if err != nil {
		return
	}
	_, _ = windows.MessageBox(0, messagePtr, titlePtr, windows.MB_ICONERROR|windows.MB_SETFOREGROUND)
}
