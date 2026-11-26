// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { KeyboardEvent, MouseEvent } from "react";

export function clickableNodeProps(handler?: (e: MouseEvent | KeyboardEvent) => void) {
    const trigger = (e: MouseEvent | KeyboardEvent) => {
        e.stopPropagation();
        handler?.(e);
    };
    return {
        role: "button" as const,
        tabIndex: 0,
        onClick: (e: MouseEvent) => trigger(e),
        onKeyDown: (e: KeyboardEvent) => {
            if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                trigger(e);
            }
        },
    };
}
