// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import { userEvent } from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { Table, TableBody } from "@/ui/table";
import type { EvalExample } from "@/types/eval";

import { ExampleEditorRow, NewExampleRow } from "@/features/evaluations/components/ExampleEditorRows";

const inTable = (row: React.ReactNode) =>
    render(
        <Table>
            <TableBody>{row}</TableBody>
        </Table>
    );

const example = (over: Partial<EvalExample> = {}): EvalExample => ({ id: "ex-1", prompt: "review the attached", expectedResponse: "looks fine", ...over }) as EvalExample;

const yaml = () => new File(["name: x"], "config.yaml", { type: "application/yaml" });

describe("ExampleEditorRow", () => {
    test("labels delete by the dataset position it was given", async () => {
        const onDelete = vi.fn();
        inTable(<ExampleEditorRow example={example()} position={14} onInlineUpdate={vi.fn()} onUploadArtifact={vi.fn()} onRemoveArtifact={vi.fn()} onDownloadArtifact={vi.fn()} onDelete={onDelete} />);

        await userEvent.click(screen.getByLabelText("Delete example #14"));
        expect(onDelete).toHaveBeenCalledTimes(1);
    });

    test.each([
        ["review the attached", "prompt"],
        ["looks fine", "expectedResponse"],
    ])("routes an inline edit of %s to the %s field", async (current, field) => {
        const onInlineUpdate = vi.fn().mockResolvedValue(undefined);
        inTable(<ExampleEditorRow example={example({ id: "ex-7" })} position={1} onInlineUpdate={onInlineUpdate} onUploadArtifact={vi.fn()} onRemoveArtifact={vi.fn()} onDownloadArtifact={vi.fn()} onDelete={vi.fn()} />);

        await userEvent.click(screen.getByText(current));
        const box = screen.getByRole("textbox") as HTMLTextAreaElement;
        await userEvent.clear(box);
        await userEvent.type(box, "edited{Enter}");

        await waitFor(() => expect(onInlineUpdate).toHaveBeenCalledWith("ex-7", field, "edited"));
    });

    test("removes and downloads against its own example", async () => {
        const onRemoveArtifact = vi.fn().mockResolvedValue(undefined);
        const onDownloadArtifact = vi.fn().mockResolvedValue(undefined);
        const withFile = example({ id: "ex-9", inputArtifact: { filename: "config.yaml", version: 0 } });
        inTable(<ExampleEditorRow example={withFile} position={1} onInlineUpdate={vi.fn()} onUploadArtifact={vi.fn()} onRemoveArtifact={onRemoveArtifact} onDownloadArtifact={onDownloadArtifact} onDelete={vi.fn()} />);

        await userEvent.click(screen.getByText("config.yaml"));
        await waitFor(() => expect(onDownloadArtifact).toHaveBeenCalledWith(withFile, "input"));

        await userEvent.click(screen.getByRole("button", { name: /remove/i }));
        await waitFor(() => expect(onRemoveArtifact).toHaveBeenCalledWith("ex-9", "input"));
    });

    test("uploads against its own example id", async () => {
        const onUploadArtifact = vi.fn().mockResolvedValue(undefined);
        inTable(<ExampleEditorRow example={example({ id: "ex-42" })} position={1} onInlineUpdate={vi.fn()} onUploadArtifact={onUploadArtifact} onRemoveArtifact={vi.fn()} onDownloadArtifact={vi.fn()} onDelete={vi.fn()} />);

        await userEvent.upload(screen.getByLabelText("Attach input file") as HTMLInputElement, yaml());

        await waitFor(() => expect(onUploadArtifact).toHaveBeenCalledWith("ex-42", "input", expect.any(File)));
    });
});

describe("NewExampleRow", () => {
    const base = {
        prompt: "",
        onPromptChange: vi.fn(),
        expected: "",
        onExpectedChange: vi.fn(),
        file: null,
        onFileChange: vi.fn(),
        expectedFile: null,
        onExpectedFileChange: vi.fn(),
        saving: false,
        error: null,
        onSave: vi.fn(),
        onCancel: vi.fn(),
    };

    test("hands the picked file to the parent instead of uploading", async () => {
        const onFileChange = vi.fn();
        inTable(<NewExampleRow {...base} onFileChange={onFileChange} />);

        await userEvent.upload(screen.getByLabelText("Attach input file") as HTMLInputElement, yaml());

        await waitFor(() => expect(onFileChange).toHaveBeenCalledWith(expect.any(File)));
    });

    test("clears the draft file on remove", async () => {
        const onFileChange = vi.fn();
        inTable(<NewExampleRow {...base} file={yaml()} onFileChange={onFileChange} />);

        await userEvent.click(screen.getByRole("button", { name: /remove/i }));

        await waitFor(() => expect(onFileChange).toHaveBeenCalledWith(null));
    });

    test("hands the picked expected file to its own handler", async () => {
        const onFileChange = vi.fn();
        const onExpectedFileChange = vi.fn();
        inTable(<NewExampleRow {...base} onFileChange={onFileChange} onExpectedFileChange={onExpectedFileChange} />);

        await userEvent.upload(screen.getByLabelText("Attach expected file") as HTMLInputElement, new File(["ref"], "ref.md", { type: "text/markdown" }));

        await waitFor(() => expect(onExpectedFileChange).toHaveBeenCalledWith(expect.any(File)));
        expect(onFileChange).not.toHaveBeenCalled();
    });

    test("reports edits to both draft fields", async () => {
        const onPromptChange = vi.fn();
        const onExpectedChange = vi.fn();
        inTable(<NewExampleRow {...base} onPromptChange={onPromptChange} onExpectedChange={onExpectedChange} />);

        await userEvent.type(screen.getByPlaceholderText("Enter prompt..."), "a");
        await userEvent.type(screen.getByLabelText("Expected response"), "b");

        expect(onPromptChange).toHaveBeenCalledWith("a");
        expect(onExpectedChange).toHaveBeenCalledWith("b");
    });

    test("keeps every field's label in view, and puts a workflow example's input before its evaluator instructions", async () => {
        const onPromptChange = vi.fn();
        inTable(<NewExampleRow {...base} inputFirst onPromptChange={onPromptChange} onInputDataChange={vi.fn()} inputData="" onExpectedPathChange={vi.fn()} onExpectedFactsChange={vi.fn()} />);

        const input = screen.getByLabelText("Input data (JSON)");
        const instructions = screen.getByLabelText("Evaluator instructions (optional)");
        expect(input.compareDocumentPosition(instructions) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
        for (const label of [
            "Input data (JSON): what the workflow receives",
            "Evaluator instructions (optional)",
            "Expected response (optional)",
            "Expected steps (JSON): steps that must run and steps that must be skipped",
            "Expected values (JSON): values the result must and must not contain",
        ]) {
            expect(screen.getByText(label)).toBeInTheDocument();
        }
        await userEvent.type(instructions, "x");
        expect(onPromptChange).toHaveBeenCalledWith("x");
        expect(screen.getByText("Evaluator instructions (optional)")).toBeVisible();
    });

    test("blocks save until a prompt is typed", () => {
        const { rerender } = inTable(<NewExampleRow {...base} />);
        expect(screen.getByRole("button", { name: "Save" }).hasAttribute("disabled")).toBe(true);

        rerender(
            <Table>
                <TableBody>
                    <NewExampleRow {...base} prompt="ask something" />
                </TableBody>
            </Table>
        );
        expect(screen.getByRole("button", { name: "Save" }).hasAttribute("disabled")).toBe(false);
    });

    test("surfaces the add error", () => {
        inTable(<NewExampleRow {...base} error="file is 20000000 bytes; the limit is 10000000 bytes" />);
        expect(screen.getByText(/the limit is 10000000 bytes/)).toBeTruthy();
    });
});
