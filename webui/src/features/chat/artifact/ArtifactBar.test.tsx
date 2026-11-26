// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { ArtifactBar } from "@/features/chat/artifact/ArtifactBar";

describe("ArtifactBar primaryLabel logic", () => {
    test("shows description as primary label when provided", () => {
        render(<ArtifactBar filename="report.pdf" description="Monthly sales report" status="completed" />);
        screen.getByText("Monthly sales report");
        screen.getByTitle("Monthly sales report");
    });

    test("shows filename when no description and filename is ≤50 chars", () => {
        render(<ArtifactBar filename="short-name.pdf" status="completed" />);
        screen.getByText("short-name.pdf");
    });

    test("truncates filename to 47 chars + ellipsis when >50 chars and no description", () => {
        const longName = "this-is-a-very-long-filename-that-exceeds-fifty-characters.pdf";
        render(<ArtifactBar filename={longName} status="completed" />);
        screen.getByText(`${longName.substring(0, 47)}...`);
    });
});

describe("ArtifactBar keyboard accessibility", () => {
    test("pressing Enter on a completed+previewable bar calls onPreview", () => {
        const onPreview = vi.fn();
        render(<ArtifactBar filename="report.pdf" status="completed" actions={{ onPreview }} />);
        const bar = screen.getByRole("button");
        fireEvent.keyDown(bar, { key: "Enter" });
        expect(onPreview).toHaveBeenCalledTimes(1);
    });

    test("pressing Space on a completed+previewable bar calls onPreview", () => {
        const onPreview = vi.fn();
        render(<ArtifactBar filename="report.pdf" status="completed" actions={{ onPreview }} />);
        const bar = screen.getByRole("button");
        fireEvent.keyDown(bar, { key: " " });
        expect(onPreview).toHaveBeenCalledTimes(1);
    });

    test("in-progress bar has no role=button and no keyboard handler", () => {
        render(<ArtifactBar filename="report.pdf" status="in-progress" />);
        expect(screen.queryByRole("button")).not.toBeInTheDocument();
    });

    test("deleted completed bar has no role=button", () => {
        const onPreview = vi.fn();
        render(<ArtifactBar filename="report.pdf" status="completed" actions={{ onPreview }} isDeleted />);
        expect(screen.queryByRole("button")).not.toBeInTheDocument();
    });
});

describe("ArtifactBar expanded content interaction", () => {
    test("clicking the header/card area opens the preview", () => {
        const onPreview = vi.fn();
        render(<ArtifactBar filename="report.pdf" status="completed" actions={{ onPreview }} expanded expandedContent={<div>expanded body text</div>} />);
        fireEvent.click(screen.getByText("report.pdf"));
        expect(onPreview).toHaveBeenCalledTimes(1);
    });

    test("clicking inside the expanded content does NOT open the preview, so a snippet can be selected and copied", () => {
        const onPreview = vi.fn();
        render(<ArtifactBar filename="report.pdf" status="completed" actions={{ onPreview }} expanded expandedContent={<div>expanded body text</div>} />);
        fireEvent.click(screen.getByText("expanded body text"));
        expect(onPreview).not.toHaveBeenCalled();
    });

    test("clicking non-selectable (media) expanded content still opens the preview", () => {
        const onPreview = vi.fn();
        render(<ArtifactBar filename="photo.png" status="completed" actions={{ onPreview }} expanded expandedContentSelectable={false} expandedContent={<div>image body</div>} />);
        fireEvent.click(screen.getByText("image body"));
        expect(onPreview).toHaveBeenCalledTimes(1);
    });
});
