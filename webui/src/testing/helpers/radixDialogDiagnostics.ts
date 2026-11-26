// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { vi } from "vitest";

const RADIX_DIALOG_DIAGNOSTIC = /`DialogContent`|`DialogTitle`|`Description`/;

const isRadixDialogDiagnostic = (args: unknown[]) => RADIX_DIALOG_DIAGNOSTIC.test(args.map(String).join(" "));

export function captureRadixDialogDiagnostics() {
    const originalError = console.error;
    const originalWarn = console.warn;
    const errorSpy = vi.spyOn(console, "error").mockImplementation((...args: unknown[]) => {
        if (!isRadixDialogDiagnostic(args)) originalError(...args);
    });
    const warnSpy = vi.spyOn(console, "warn").mockImplementation((...args: unknown[]) => {
        if (!isRadixDialogDiagnostic(args)) originalWarn(...args);
    });

    return {
        messages: () => [...errorSpy.mock.calls, ...warnSpy.mock.calls].filter(isRadixDialogDiagnostic).map(args => args.map(String).join(" ")),
        restore: () => {
            errorSpy.mockRestore();
            warnSpy.mockRestore();
        },
    };
}
