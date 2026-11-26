// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { createEvent, fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { useDragReorder } from "./useDragReorder";

function List({ initial, disabled }: { readonly initial: string[]; readonly disabled?: boolean }) {
    const [items, setItems] = useState(initial);
    const drag = useDragReorder(items, setItems, disabled);
    return (
        <ul>
            {items.map((item, i) => (
                <li key={item} data-testid={`row-${item}`} data-drop={drag.isDropTarget(i)} data-dragging={drag.isDragging(i)} {...drag.rowProps(i)}>
                    <span data-testid={`handle-${item}`} {...drag.handleProps(i)}>
                        {item}
                    </span>
                </li>
            ))}
        </ul>
    );
}

function dataTransfer() {
    const data: Record<string, string> = {};
    return { effectAllowed: "", dropEffect: "", setData: (k: string, v: string) => (data[k] = v), getData: (k: string) => data[k] };
}

function drag(from: string, to: string) {
    const dt = dataTransfer();
    fireEvent(screen.getByTestId(`handle-${from}`), Object.assign(createEvent.dragStart(screen.getByTestId(`handle-${from}`)), { dataTransfer: dt }));
    fireEvent(screen.getByTestId(`row-${to}`), Object.assign(createEvent.dragOver(screen.getByTestId(`row-${to}`)), { dataTransfer: dt }));
    return dt;
}

function order(): string[] {
    return screen.getAllByRole("listitem").map(li => li.textContent ?? "");
}

describe("useDragReorder", () => {
    test("moves the dragged row to where it is dropped, marking both rows while dragging", () => {
        render(<List initial={["a", "b", "c"]} />);
        const dt = drag("a", "c");
        expect(screen.getByTestId("row-a")).toHaveAttribute("data-dragging", "true");
        expect(screen.getByTestId("row-c")).toHaveAttribute("data-drop", "true");

        fireEvent(screen.getByTestId("row-c"), Object.assign(createEvent.drop(screen.getByTestId("row-c")), { dataTransfer: dt }));
        expect(order()).toEqual(["b", "c", "a"]);
        expect(screen.getByTestId("row-a")).toHaveAttribute("data-dragging", "false");
    });

    test("dropping a row on itself, or ending the drag, changes nothing", () => {
        render(<List initial={["a", "b"]} />);
        const dt = drag("a", "a");
        fireEvent(screen.getByTestId("row-a"), Object.assign(createEvent.drop(screen.getByTestId("row-a")), { dataTransfer: dt }));
        expect(order()).toEqual(["a", "b"]);

        drag("b", "a");
        fireEvent.dragEnd(screen.getByTestId("handle-b"));
        expect(screen.getByTestId("row-a")).toHaveAttribute("data-drop", "false");
        expect(order()).toEqual(["a", "b"]);
    });

    test("does nothing while disabled", () => {
        render(<List initial={["a", "b"]} disabled />);
        expect(screen.getByTestId("handle-a")).toHaveAttribute("draggable", "false");
        const dt = drag("a", "b");
        fireEvent(screen.getByTestId("row-b"), Object.assign(createEvent.drop(screen.getByTestId("row-b")), { dataTransfer: dt }));
        expect(order()).toEqual(["a", "b"]);
    });
});
