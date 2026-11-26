// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export function enableWailsRuntime() {
    (globalThis as { runtime?: unknown }).runtime = { BrowserOpenURL: () => {} };
}

export function disableWailsRuntime() {
    delete (globalThis as { runtime?: unknown }).runtime;
}
