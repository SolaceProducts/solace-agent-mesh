// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { PasteActionDialog } from "@/features/chat/paste/PasteActionDialog";

const CONTENT = "a".repeat(200);

describe("PasteActionDialog", () => {
    test("keeps the filename the user is typing when the parent re-renders", async () => {
        const view = (existingArtifacts: string[]) => <PasteActionDialog isOpen content={CONTENT} onSaveMetadata={vi.fn()} onCancel={vi.fn()} existingArtifacts={existingArtifacts} defaultFilename="snippet.txt" />;

        const { rerender } = render(view([]));

        const filename = await screen.findByLabelText("Filename");
        await userEvent.clear(filename);
        await userEvent.type(filename, "notes");

        rerender(view([]));

        await userEvent.type(filename, ".md");
        expect(filename).toHaveValue("notes.md");
    });

    test("seeds the filename from defaultFilename when it opens", async () => {
        render(<PasteActionDialog isOpen content={CONTENT} onSaveMetadata={vi.fn()} onCancel={vi.fn()} existingArtifacts={[]} defaultFilename="snippet-3.txt" />);

        expect(await screen.findByLabelText("Filename")).toHaveValue("snippet-3.txt");
    });

    test("seeds from initialFilename when an existing snippet is reopened", async () => {
        render(<PasteActionDialog isOpen content={CONTENT} onSaveMetadata={vi.fn()} onCancel={vi.fn()} existingArtifacts={["notes.md"]} initialFilename="notes.md" initialDescription="a description" defaultFilename="snippet.txt" />);

        expect(await screen.findByLabelText("Filename")).toHaveValue("notes.md");
        expect(screen.getByLabelText("Description (optional)")).toHaveValue("a description");
    });

    test("restores the form when the same snippet is closed and reopened", async () => {
        const view = (isOpen: boolean, content: string) => <PasteActionDialog isOpen={isOpen} content={content} onSaveMetadata={vi.fn()} onCancel={vi.fn()} existingArtifacts={[]} defaultFilename="snippet-2.txt" />;

        const { rerender } = render(view(true, CONTENT));
        const filename = await screen.findByLabelText("Filename");
        await userEvent.clear(filename);
        await userEvent.type(filename, "notes");

        await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
        rerender(view(false, ""));
        rerender(view(true, CONTENT));

        expect(await screen.findByLabelText("Filename")).toHaveValue("snippet-2.txt");
        expect(screen.getByLabelText("Content")).toHaveValue(CONTENT);
    });

    test("re-seeds when a different snippet is opened", async () => {
        const view = (content: string, defaultFilename: string) => <PasteActionDialog isOpen content={content} onSaveMetadata={vi.fn()} onCancel={vi.fn()} existingArtifacts={[]} defaultFilename={defaultFilename} />;

        const { rerender } = render(view(CONTENT, "snippet.txt"));
        expect(await screen.findByLabelText("Filename")).toHaveValue("snippet.txt");

        rerender(view("b".repeat(200), "snippet-2.txt"));
        expect(await screen.findByLabelText("Filename")).toHaveValue("snippet-2.txt");
    });
});
