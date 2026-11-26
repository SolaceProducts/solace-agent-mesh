// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { SelectionContextMenu } from "./SelectionContextMenu";

const pos = { x: 120, y: 80 };

describe("SelectionContextMenu", () => {
    let followUp: ReturnType<typeof vi.fn>;

    beforeEach(() => {
        followUp = vi.fn();
        window.addEventListener("follow-up-question", followUp as EventListener);
    });

    afterEach(() => {
        window.removeEventListener("follow-up-question", followUp as EventListener);
    });

    test("labels the action 'Add Comment', not the old 'Ask Followup'", () => {
        render(<SelectionContextMenu isOpen position={pos} selectedText="hello world" sourceTaskId="task-1" onClose={vi.fn()} />);
        expect(screen.getByRole("button", { name: /add comment/i })).toBeInTheDocument();
        expect(screen.queryByText(/ask followup/i)).toBeNull();
    });

    test("clicking dispatches follow-up-question with the selected text + source, then closes", () => {
        const onClose = vi.fn();
        render(<SelectionContextMenu isOpen position={pos} selectedText="quoted text" sourceTaskId="task-42" onClose={onClose} />);

        fireEvent.click(screen.getByRole("button", { name: /add comment/i }));

        expect(followUp).toHaveBeenCalledTimes(1);
        const detail = (followUp.mock.calls[0][0] as CustomEvent).detail;
        expect(detail.text).toBe("quoted text");
        expect(detail.sourceMessageId).toBe("task-42");
        expect(detail.prompt).toBe("");
        expect(detail.autoSubmit).toBe(false);
        expect(onClose).toHaveBeenCalledTimes(1);
    });

    test("renders nothing when closed", () => {
        const { container } = render(<SelectionContextMenu isOpen={false} position={pos} selectedText="x" sourceTaskId={null} onClose={vi.fn()} />);
        expect(container).toBeEmptyDOMElement();
    });
});
