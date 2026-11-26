// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export function isWailsRuntime(): boolean {
    return Boolean((globalThis as unknown as { runtime?: { BrowserOpenURL?: (url: string) => void } }).runtime?.BrowserOpenURL);
}

export function newTabProps(): { target?: "_blank"; rel?: string } {
    return isWailsRuntime() ? {} : { target: "_blank", rel: "noopener noreferrer" };
}

export function openExternalLink(url: string): void {
    const wailsRuntime = (globalThis as unknown as { runtime?: { BrowserOpenURL?: (url: string) => void } }).runtime;
    if (wailsRuntime?.BrowserOpenURL) {
        wailsRuntime.BrowserOpenURL(url);
        return;
    }
    globalThis.open(url, "_blank", "noopener,noreferrer");
}
