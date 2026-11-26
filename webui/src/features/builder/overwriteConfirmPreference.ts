// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const SKIP_CONFIRM_KEY = "sam_builder_skip_overwrite_confirm";

export function shouldSkipOverwriteConfirm(): boolean {
    try {
        return localStorage.getItem(SKIP_CONFIRM_KEY) === "true";
    } catch {
        return false;
    }
}

export function persistSkipOverwriteConfirm(): void {
    try {
        localStorage.setItem(SKIP_CONFIRM_KEY, "true");
    } catch {}
}
