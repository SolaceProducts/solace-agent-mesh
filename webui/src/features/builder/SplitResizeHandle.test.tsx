// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useRef } from "react";
import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { SplitResizeHandle } from "./SplitResizeHandle";

function PanelWithHandle() {
    const panelRef = useRef<HTMLDivElement>(null);
    return (
        <div ref={panelRef} data-testid="panel">
            <SplitResizeHandle panelRef={panelRef} onDragStart={vi.fn()} onWidthChange={vi.fn()} />
        </div>
    );
}

describe("SplitResizeHandle where ResizeObserver does not exist", () => {
    const original = globalThis.ResizeObserver;

    beforeEach(() => {
        Reflect.deleteProperty(globalThis, "ResizeObserver");
        vi.stubGlobal("innerWidth", 1440);
        vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({ width: 400 } as DOMRect);
    });

    afterEach(() => {
        globalThis.ResizeObserver = original;
        vi.unstubAllGlobals();
        vi.restoreAllMocks();
    });

    test("still reports the panel width and bounds from mount", () => {
        render(<PanelWithHandle />);

        const handle = screen.getByRole("separator", { name: "Resize chat panel" });
        expect(handle).toHaveAttribute("aria-valuenow", "400");
        expect(handle).toHaveAttribute("aria-valuemax", "864");
    });
});
