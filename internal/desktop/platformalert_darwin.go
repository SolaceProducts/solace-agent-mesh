// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build desktop

package desktop

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation -framework AppKit

#include <stdlib.h>

#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>

// showStartupAlert runs a modal NSAlert with no parent window. Wails has no
// pre-window dialog path on macOS (its MessageDialog needs the window
// context, which only exists after wails.Run), so the alert drives its own
// modal session. Running directly on the calling thread is safe: the Wails
// darwin frontend package init locks the main goroutine to the main thread,
// and this is called before wails.Run starts the run loop.
static void showStartupAlert(const char* title, const char* message) {
	@autoreleasepool {
		[NSApplication sharedApplication];
		NSAlert *alert = [[NSAlert alloc] init];
		[alert setAlertStyle:NSAlertStyleCritical];
		[alert setMessageText:[NSString stringWithUTF8String:title]];
		[alert setInformativeText:[NSString stringWithUTF8String:message]];
		[alert addButtonWithTitle:@"Quit"];
		[NSApp activateIgnoringOtherApps:YES];
		[alert runModal];
	}
}
*/
import "C"

import "unsafe"

func showPlatformAlert(title, message string) {
	cTitle := C.CString(title)
	defer C.free(unsafe.Pointer(cTitle))
	cMessage := C.CString(message)
	defer C.free(unsafe.Pointer(cMessage))
	C.showStartupAlert(cTitle, cMessage)
}
