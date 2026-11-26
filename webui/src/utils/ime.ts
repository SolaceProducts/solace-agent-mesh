// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { KeyboardEvent } from "react";

const LEGACY_IME_KEY_CODE = 229;

interface LegacyKeyCode {
    keyCode?: number;
}

export function isNativeImeComposing(event: globalThis.KeyboardEvent): boolean {
    return event.isComposing || (event as LegacyKeyCode).keyCode === LEGACY_IME_KEY_CODE;
}

export function isImeComposing(event: KeyboardEvent): boolean {
    return isNativeImeComposing(event.nativeEvent);
}
