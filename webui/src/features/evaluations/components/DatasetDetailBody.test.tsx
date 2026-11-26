// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { userEvent } from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { Dialog, DialogContent } from "@/ui/dialog";
import type { ConfigSnapshotDataset } from "@/types/eval";

import { DatasetDetailBody } from "@/features/evaluations/components/RunDetailsAccordion";

function renderBody(snap: ConfigSnapshotDataset) {
    return render(
        <Dialog open onOpenChange={() => {}}>
            <DialogContent>
                <DatasetDetailBody snapshot={snap} onClose={vi.fn()} />
            </DialogContent>
        </Dialog>
    );
}

type SnapshotExample = NonNullable<ConfigSnapshotDataset["examples"]>[number];

function example(id: string, prompt: string, response?: string): SnapshotExample {
    return { id, sequence_number: parseInt(id, 10), prompt, expected_response: response ?? null };
}

function snapshot(examples: SnapshotExample[]): ConfigSnapshotDataset {
    return { id: "ds", name: "Fixture Dataset", example_count: examples.length, examples };
}

describe("DatasetDetailBody", () => {
    test("renders the snapshot's rows, not the live dataset", () => {
        const snap = snapshot([example("1", "What is 2 + 2?", "4"), example("2", "Where is Ottawa located?", "Canada")]);
        renderBody(snap);

        expect(screen.getByText("What is 2 + 2?")).toBeTruthy();
        expect(screen.getByText("4")).toBeTruthy();
        expect(screen.getByText("Where is Ottawa located?")).toBeTruthy();
        expect(screen.getByText("Canada")).toBeTruthy();
    });

    test("shows a workflow example's input data, and heads the column for it", () => {
        const snap = snapshot([{ ...example("1", ""), input_data: { orderId: "O-1" } }, example("2", "")]);
        renderBody(snap);

        expect(screen.getByText("Input data")).toBeTruthy();
        expect(screen.getByText('{"orderId":"O-1"}')).toBeTruthy();
    });

    test("shows empty state when the snapshot has no examples", () => {
        const snap: ConfigSnapshotDataset = { id: "ds", name: "Empty", example_count: 0, examples: [] };
        renderBody(snap);

        expect(screen.getByText("No examples in this dataset.")).toBeTruthy();
    });

    test("client-side paginates when the snapshot exceeds the page size", async () => {
        const rows = Array.from({ length: 7 }, (_, i) => example(String(i + 1), `Prompt ${i + 1}`));
        renderBody(snapshot(rows));

        expect(screen.getByText("Prompt 1")).toBeTruthy();
        expect(screen.getByText("Prompt 5")).toBeTruthy();
        expect(screen.queryByText("Prompt 6")).toBeNull();
        expect(screen.getByText(/Showing 1–5 of 7/)).toBeTruthy();

        await userEvent.click(screen.getByLabelText("Go to next page"));

        expect(screen.getByText("Prompt 6")).toBeTruthy();
        expect(screen.getByText("Prompt 7")).toBeTruthy();
        expect(screen.queryByText("Prompt 1")).toBeNull();
        expect(screen.getByText(/Showing 6–7 of 7/)).toBeTruthy();
    });

    test("shows the count for single-page datasets too", () => {
        const snap = snapshot([example("1", "One", "Two")]);
        renderBody(snap);

        expect(screen.getByText(/Showing 1 results/)).toBeTruthy();
    });

    test("shows the files column from the snapshot's own artifact fields", () => {
        const withFile: SnapshotExample = {
            ...example("1", "Review this config"),
            artifact: { filename: "values.yaml", version: 0, size_bytes: 84 },
            expected_artifact: { filename: "reference.md", version: 0, size_bytes: 12 },
        };
        renderBody(snapshot([withFile, example("2", "No file here")]));

        expect(screen.getByText("Input File")).toBeTruthy();
        expect(screen.getByText("Expected File")).toBeTruthy();
        expect(screen.getByText("values.yaml")).toBeTruthy();
        expect(screen.getByText("reference.md")).toBeTruthy();
    });

    test("shows the file columns for an expected-only snapshot", () => {
        const withExpected: SnapshotExample = { ...example("1", "Prompt"), expected_artifact: { filename: "ref.md", version: 0 } };
        renderBody(snapshot([withExpected]));

        expect(screen.getByText("Input File")).toBeTruthy();
        expect(screen.getByText("Expected File")).toBeTruthy();
        expect(screen.getByText("ref.md")).toBeTruthy();
    });

    test("omits the file columns when no example carries one", () => {
        renderBody(snapshot([example("1", "Plain prompt", "Plain answer")]));

        expect(screen.queryByText("Input File")).toBeNull();
        expect(screen.queryByText("Expected File")).toBeNull();
    });
});
