// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build desktop

package desktop

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation -framework AppKit

#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>

// openFilePanel presents an NSOpenPanel as a sheet attached to the key
// window. Using a sheet guarantees the dialog is visible (it slides down
// from the title bar) and doesn't block the main run loop.
//
// Returns the selected file path (caller must free), or NULL if cancelled.
static const char* openFilePanel(const char* title) {
	__block const char* result = NULL;
	dispatch_semaphore_t sem = dispatch_semaphore_create(0);

	dispatch_async(dispatch_get_main_queue(), ^{
		NSOpenPanel *panel = [NSOpenPanel openPanel];
		if (title != NULL) {
			[panel setTitle:[NSString stringWithUTF8String:title]];
		}
		[panel setCanChooseFiles:YES];
		[panel setCanChooseDirectories:NO];
		[panel setAllowsMultipleSelection:NO];
		[panel setResolvesAliases:YES];

		void (^handler)(NSModalResponse) = ^(NSModalResponse response) {
			if (response == NSModalResponseOK) {
				NSURL *url = [[panel URLs] firstObject];
				if (url != nil) {
					result = strdup([[url path] UTF8String]);
				}
			}
			dispatch_semaphore_signal(sem);
		};

		// Attach as a sheet to the key window so the dialog can't
		// appear behind the Wails WebView window.
		NSWindow *parent = [NSApp keyWindow];
		if (parent != nil) {
			[parent makeKeyAndOrderFront:nil];
			[panel beginSheetModalForWindow:parent completionHandler:handler];
		} else {
			[NSApp activateIgnoringOtherApps:YES];
			[panel beginWithCompletionHandler:handler];
		}
	});

	dispatch_semaphore_wait(sem, DISPATCH_TIME_FOREVER);
	return result;
}

// openFolderPanel presents an NSOpenPanel configured for directory
// selection. Same sheet-modal pattern as openFilePanel — the dialog
// slides down from the parent window's title bar.
//
// Returns the selected directory path (caller must free), or NULL if
// cancelled. Used by the WebUI CreateVolumeDialog to let the user pick
// a host directory for linked / copy-from-dir volumes without typing
// the absolute path.
static const char* openFolderPanel(const char* title) {
	__block const char* result = NULL;
	dispatch_semaphore_t sem = dispatch_semaphore_create(0);

	dispatch_async(dispatch_get_main_queue(), ^{
		NSOpenPanel *panel = [NSOpenPanel openPanel];
		if (title != NULL) {
			[panel setTitle:[NSString stringWithUTF8String:title]];
		}
		[panel setCanChooseFiles:NO];
		[panel setCanChooseDirectories:YES];
		[panel setAllowsMultipleSelection:NO];
		[panel setResolvesAliases:YES];
		[panel setCanCreateDirectories:YES];

		void (^handler)(NSModalResponse) = ^(NSModalResponse response) {
			if (response == NSModalResponseOK) {
				NSURL *url = [[panel URLs] firstObject];
				if (url != nil) {
					result = strdup([[url path] UTF8String]);
				}
			}
			dispatch_semaphore_signal(sem);
		};

		NSWindow *parent = [NSApp keyWindow];
		if (parent != nil) {
			[parent makeKeyAndOrderFront:nil];
			[panel beginSheetModalForWindow:parent completionHandler:handler];
		} else {
			[NSApp activateIgnoringOtherApps:YES];
			[panel beginWithCompletionHandler:handler];
		}
	});

	dispatch_semaphore_wait(sem, DISPATCH_TIME_FOREVER);
	return result;
}

// saveFilePanel presents an NSSavePanel as a sheet attached to the key
// window. Using a sheet guarantees the dialog is visible (it slides down
// from the title bar) and doesn't block the main run loop.
//
// defaultName is the suggested filename. defaultDir is the initial directory
// (pass NULL to use the system default).
// Returns the selected save path (caller must free), or NULL if cancelled.
static const char* saveFilePanel(const char* title, const char* defaultName, const char* defaultDir) {
	__block const char* result = NULL;
	dispatch_semaphore_t sem = dispatch_semaphore_create(0);

	dispatch_async(dispatch_get_main_queue(), ^{
		NSSavePanel *panel = [NSSavePanel savePanel];
		if (title != NULL) {
			[panel setTitle:[NSString stringWithUTF8String:title]];
		}
		if (defaultName != NULL) {
			[panel setNameFieldStringValue:[NSString stringWithUTF8String:defaultName]];
		}
		if (defaultDir != NULL) {
			[panel setDirectoryURL:[NSURL fileURLWithPath:[NSString stringWithUTF8String:defaultDir]]];
		}
		[panel setExtensionHidden:NO];
		[panel setCanCreateDirectories:YES];

		void (^handler)(NSModalResponse) = ^(NSModalResponse response) {
			if (response == NSModalResponseOK) {
				NSURL *url = [panel URL];
				if (url != nil) {
					result = strdup([[url path] UTF8String]);
				}
			}
			dispatch_semaphore_signal(sem);
		};

		// Attach as a sheet to the key window so the dialog can't
		// appear behind the Wails WebView window.
		NSWindow *parent = [NSApp keyWindow];
		if (parent != nil) {
			[parent makeKeyAndOrderFront:nil];
			[panel beginSheetModalForWindow:parent completionHandler:handler];
		} else {
			[NSApp activateIgnoringOtherApps:YES];
			[panel beginWithCompletionHandler:handler];
		}
	});

	dispatch_semaphore_wait(sem, DISPATCH_TIME_FOREVER);
	return result;
}
*/
import "C"

import (
	"context"
	"unsafe"
)

func nativeOpenFileDialog(_ context.Context, title string) (string, error) {
	cTitle := C.CString(title)
	defer C.free(unsafe.Pointer(cTitle))

	cResult := C.openFilePanel(cTitle)
	if cResult == nil {
		return "", nil
	}
	defer C.free(unsafe.Pointer(cResult))
	return C.GoString(cResult), nil
}

func nativeOpenFolderDialog(_ context.Context, title string) (string, error) {
	cTitle := C.CString(title)
	defer C.free(unsafe.Pointer(cTitle))

	cResult := C.openFolderPanel(cTitle)
	if cResult == nil {
		return "", nil
	}
	defer C.free(unsafe.Pointer(cResult))
	return C.GoString(cResult), nil
}

func nativeSaveFileDialog(_ context.Context, title, defaultName, defaultDir string) (string, error) {
	cTitle := C.CString(title)
	defer C.free(unsafe.Pointer(cTitle))
	cName := C.CString(defaultName)
	defer C.free(unsafe.Pointer(cName))

	var cDir *C.char
	if defaultDir != "" {
		cDir = C.CString(defaultDir)
		defer C.free(unsafe.Pointer(cDir))
	}

	cResult := C.saveFilePanel(cTitle, cName, cDir)
	if cResult == nil {
		return "", nil
	}
	defer C.free(unsafe.Pointer(cResult))
	return C.GoString(cResult), nil
}
