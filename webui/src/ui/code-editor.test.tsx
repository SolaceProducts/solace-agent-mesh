// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { CodeEditor } from "./code-editor";

describe("CodeEditor", () => {
    test("renders one gutter line number per line of content", () => {
        const { container } = render(<CodeEditor value={"a\nb\nc"} onChange={vi.fn()} aria-label="Code" />);
        const gutter = container.querySelector('[aria-hidden="true"]');
        expect(gutter?.textContent).toBe("123");
    });

    test("always shows at least one line number when empty", () => {
        const { container } = render(<CodeEditor value="" onChange={vi.fn()} aria-label="Code" />);
        const gutter = container.querySelector('[aria-hidden="true"]');
        expect(gutter?.textContent).toBe("1");
    });

    test("grows the gutter as new lines are added", () => {
        const { container, rerender } = render(<CodeEditor value={"one"} onChange={vi.fn()} aria-label="Code" />);
        expect(container.querySelector('[aria-hidden="true"]')?.textContent).toBe("1");
        rerender(<CodeEditor value={"one\ntwo\nthree\nfour"} onChange={vi.fn()} aria-label="Code" />);
        expect(container.querySelector('[aria-hidden="true"]')?.textContent).toBe("1234");
    });

    test("forwards edits through onChange", async () => {
        const onChange = vi.fn();
        render(<CodeEditor value="" onChange={onChange} aria-label="Code" />);
        await userEvent.type(screen.getByLabelText("Code"), "x");
        expect(onChange).toHaveBeenCalled();
    });
});
