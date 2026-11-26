// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { Menu } from "./menu";

function renderMenu(disabled: boolean | undefined) {
    const onClick = vi.fn();
    render(<Menu actions={[{ id: "a", label: "Action", disabled, onClick }]} />);
    return { item: screen.getByRole("menuitem"), onClick };
}

describe("Menu disabled items", () => {
    test("a disabled item is marked disabled and ignores clicks, Enter and Space", () => {
        const { item, onClick } = renderMenu(true);
        expect(item).toHaveAttribute("data-disabled");
        expect(item).toHaveAttribute("aria-disabled", "true");
        fireEvent.click(item);
        fireEvent.keyDown(item, { key: "Enter" });
        fireEvent.keyDown(item, { key: " " });
        expect(onClick).not.toHaveBeenCalled();
    });

    test("disabled: false omits data-disabled (a false value would still match the data-[disabled] style) and stays clickable", () => {
        const { item, onClick } = renderMenu(false);
        expect(item).not.toHaveAttribute("data-disabled");
        expect(item).not.toHaveAttribute("aria-disabled");
        fireEvent.click(item);
        fireEvent.keyDown(item, { key: "Enter" });
        expect(onClick).toHaveBeenCalledTimes(2);
    });
});
