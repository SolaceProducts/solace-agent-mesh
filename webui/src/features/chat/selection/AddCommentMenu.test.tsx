// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";

import { AddCommentMenu } from "./AddCommentMenu";

const pos = { x: 100, y: 100 };

describe("AddCommentMenu", () => {
    afterEach(() => {
        vi.useRealTimers();
    });

    test("renders nothing when closed", () => {
        const { container } = render(<AddCommentMenu isOpen={false} position={pos} onAddComment={vi.fn()} onClose={vi.fn()} />);
        expect(container).toBeEmptyDOMElement();
    });

    test("renders nothing when open but position is null", () => {
        const { container } = render(<AddCommentMenu isOpen position={null} onAddComment={vi.fn()} onClose={vi.fn()} />);
        expect(container).toBeEmptyDOMElement();
    });

    test("shows the 'Add Comment' label and the message-square-plus icon when open", () => {
        const { container } = render(<AddCommentMenu isOpen position={pos} onAddComment={vi.fn()} onClose={vi.fn()} />);
        expect(screen.getByRole("button", { name: /add comment/i })).toBeInTheDocument();
        expect(container.querySelector("svg.lucide-message-square-plus")).toBeInTheDocument();
    });

    test("moves focus into the popover on open and restores it to the opener on close", () => {
        const anchor = document.createElement("button");
        document.body.appendChild(anchor);
        anchor.focus();

        const { rerender } = render(<AddCommentMenu isOpen position={pos} onAddComment={vi.fn()} onClose={vi.fn()} />);
        expect(screen.getByRole("button", { name: /add comment/i })).toHaveFocus();

        rerender(<AddCommentMenu isOpen={false} position={pos} onAddComment={vi.fn()} onClose={vi.fn()} />);
        expect(anchor).toHaveFocus();

        anchor.remove();
    });

    test("re-applies a live text selection after focusing on open (mouseup selection survives)", () => {
        const p = document.createElement("p");
        p.textContent = "hello world";
        document.body.appendChild(p);
        const range = document.createRange();
        range.selectNodeContents(p);
        const selection = window.getSelection()!;
        selection.removeAllRanges();
        selection.addRange(range);

        const addRangeSpy = vi.spyOn(selection, "addRange");

        render(<AddCommentMenu isOpen position={pos} onAddComment={vi.fn()} onClose={vi.fn()} />);

        expect(screen.getByRole("button", { name: /add comment/i })).toHaveFocus();
        expect(addRangeSpy).toHaveBeenCalledTimes(1);
        expect(selection.toString()).toBe("hello world");

        addRangeSpy.mockRestore();
        p.remove();
    });

    test("leaves the selection untouched when there is none (Quick Build click-open)", () => {
        const selection = window.getSelection()!;
        selection.removeAllRanges();
        const removeAllRangesSpy = vi.spyOn(selection, "removeAllRanges");

        render(<AddCommentMenu isOpen position={pos} onAddComment={vi.fn()} onClose={vi.fn()} />);

        expect(screen.getByRole("button", { name: /add comment/i })).toHaveFocus();
        expect(removeAllRangesSpy).not.toHaveBeenCalled();

        removeAllRangesSpy.mockRestore();
    });

    test("closes on window resize", () => {
        vi.useFakeTimers();
        const onClose = vi.fn();
        render(<AddCommentMenu isOpen position={pos} onAddComment={vi.fn()} onClose={onClose} />);
        vi.advanceTimersByTime(150);
        fireEvent(window, new Event("resize"));
        expect(onClose).toHaveBeenCalledTimes(1);
    });

    test("clicking the button calls onAddComment and leaves dismissal to the parent", () => {
        const onAddComment = vi.fn();
        const onClose = vi.fn();
        render(<AddCommentMenu isOpen position={pos} onAddComment={onAddComment} onClose={onClose} />);
        fireEvent.click(screen.getByRole("button", { name: /add comment/i }));
        expect(onAddComment).toHaveBeenCalledTimes(1);
        expect(onClose).not.toHaveBeenCalled();
    });

    test("pressing Escape calls onClose", () => {
        vi.useFakeTimers();
        const onClose = vi.fn();
        render(<AddCommentMenu isOpen position={pos} onAddComment={vi.fn()} onClose={onClose} />);
        vi.advanceTimersByTime(150);
        fireEvent.keyDown(document, { key: "Escape" });
        expect(onClose).toHaveBeenCalledTimes(1);
    });

    test("mousedown outside the menu calls onClose", () => {
        vi.useFakeTimers();
        const onClose = vi.fn();
        render(<AddCommentMenu isOpen position={pos} onAddComment={vi.fn()} onClose={onClose} />);
        vi.advanceTimersByTime(150);
        fireEvent.mouseDown(document.body);
        expect(onClose).toHaveBeenCalledTimes(1);
    });

    test("scrolling calls onClose", () => {
        vi.useFakeTimers();
        const onClose = vi.fn();
        render(<AddCommentMenu isOpen position={pos} onAddComment={vi.fn()} onClose={onClose} />);
        vi.advanceTimersByTime(150);
        fireEvent.scroll(window);
        expect(onClose).toHaveBeenCalledTimes(1);
    });

    test("still attaches dismissal listeners when the parent re-renders with a fresh onClose before the attach delay elapses", () => {
        vi.useFakeTimers();
        const onCloseA = vi.fn();
        const onCloseB = vi.fn();
        const onCloseC = vi.fn();
        const { rerender } = render(<AddCommentMenu isOpen position={pos} onAddComment={vi.fn()} onClose={onCloseA} />);
        vi.advanceTimersByTime(60);
        rerender(<AddCommentMenu isOpen position={pos} onAddComment={vi.fn()} onClose={onCloseB} />);
        vi.advanceTimersByTime(60);
        rerender(<AddCommentMenu isOpen position={pos} onAddComment={vi.fn()} onClose={onCloseC} />);
        vi.advanceTimersByTime(60);

        fireEvent.keyDown(document, { key: "Escape" });

        expect(onCloseC).toHaveBeenCalledTimes(1);
        expect(onCloseA).not.toHaveBeenCalled();
        expect(onCloseB).not.toHaveBeenCalled();
    });
});
