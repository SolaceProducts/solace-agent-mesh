// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { PastedTextBadge, PendingPastedTextBadge } from "@/features/chat/paste/PastedTextBadge";

describe("PastedTextBadge", () => {
    test("renders the pasted text index label", () => {
        render(<PastedTextBadge index={1} textPreview="hello" onClick={vi.fn()} />);
        expect(screen.getByText("Pasted Text #1")).toBeInTheDocument();
    });

    test("shows remove button when onRemove is provided; clicking remove calls onRemove, not onClick", () => {
        const onClick = vi.fn();
        const onRemove = vi.fn();
        render(<PastedTextBadge index={2} textPreview="hi" onClick={onClick} onRemove={onRemove} />);
        const removeBtn = screen.getByTitle("Remove pasted text");
        fireEvent.click(removeBtn);
        expect(onRemove).toHaveBeenCalledTimes(1);
        expect(onClick).not.toHaveBeenCalled();
    });
});

describe("PendingPastedTextBadge", () => {
    test("shows default filename label when not configured", () => {
        render(<PendingPastedTextBadge content="some text" onClick={vi.fn()} onRemove={vi.fn()} isConfigured={false} defaultFilename="snippet.txt" />);
        expect(screen.getByText("snippet.txt")).toBeInTheDocument();
    });

    test("shows configured filename with blue badge when isConfigured is true", () => {
        render(<PendingPastedTextBadge content="some text" onClick={vi.fn()} onRemove={vi.fn()} isConfigured={true} filename="report.txt" />);
        expect(screen.getByText("report.txt")).toBeInTheDocument();
    });
});
