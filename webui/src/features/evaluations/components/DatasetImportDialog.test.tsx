// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { DatasetImportDialog } from "./DatasetImportDialog";

const BASE = "/api/v2/platform/evaluations";
const CSV = "prompt,expected_response\nhi,hello\n";

const dataset = (id: string, name: string) => ({ id, type: "dataset", name, description: null, exampleCount: 0, experimentCount: 0, createdBy: "u", updatedBy: "u", createdTime: null, updatedTime: null });

function csvFile(name: string) {
    return Object.assign(new File([CSV], name, { type: "text/csv" }), { text: async () => CSV });
}

describe("DatasetImportDialog", () => {
    const fetchMock = vi.fn<typeof fetch>();

    beforeEach(() => {
        vi.stubGlobal("fetch", fetchMock);
    });

    afterEach(() => {
        fetchMock.mockReset();
        vi.unstubAllGlobals();
    });

    test("renames the dataset on a 409 and imports the file as multipart", async () => {
        const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status });
        fetchMock
            .mockResolvedValueOnce(json(409, { message: "Unable to create dataset", errorId: "e1" }))
            .mockImplementationOnce(async (_url, init) => json(201, { data: dataset("ds-2", JSON.parse(init?.body as string).name) }))
            .mockResolvedValueOnce(json(200, { data: { created: 1, skipped: 0, dropped: 0, limit: 5000 } }));
        const onImported = vi.fn();

        renderWithProviders(<DatasetImportDialog open onClose={vi.fn()} onImported={onImported} />);
        fireEvent.change(document.querySelector('input[name="dataset-file"]') as HTMLInputElement, { target: { files: [csvFile("golden.csv")] } });
        fireEvent.click(await screen.findByRole("button", { name: "Import" }));

        await waitFor(() => expect(onImported).toHaveBeenCalled());
        const path = (n: number) => new URL(String(fetchMock.mock.calls[n][0])).pathname;
        const [init0, init1, init2] = fetchMock.mock.calls.map(c => c[1]);
        expect([path(0), init0?.method, JSON.parse(init0?.body as string)]).toEqual([`${BASE}/datasets`, "POST", { name: "golden", description: "" }]);
        expect([path(1), init1?.method]).toEqual([`${BASE}/datasets`, "POST"]);
        const renamed = JSON.parse(init1?.body as string).name;
        expect(renamed).toMatch(/^golden \(.{8}\)$/);
        expect([path(2), init2?.method]).toEqual([`${BASE}/datasets/ds-2/examples/import`, "PUT"]);
        expect(((init2?.body as FormData).get("file") as File).name).toBe("examples.csv");
        expect(onImported).toHaveBeenCalledWith("ds-2", renamed);
    });

    test("deletes the dataset it created when the file is refused, and says why", async () => {
        const json = (status: number, body?: unknown) => new Response(body === undefined ? null : JSON.stringify(body), { status });
        fetchMock
            .mockResolvedValueOnce(json(201, { data: dataset("ds-3", "golden") }))
            .mockResolvedValueOnce(json(422, { message: "Unable to import examples", errorId: "e1", validationDetails: { file: ["row 2 is not valid CSV"] } }))
            .mockResolvedValueOnce(json(204));
        const onImported = vi.fn();

        renderWithProviders(<DatasetImportDialog open onClose={vi.fn()} onImported={onImported} />);
        fireEvent.change(document.querySelector('input[name="dataset-file"]') as HTMLInputElement, { target: { files: [csvFile("golden.csv")] } });
        fireEvent.click(await screen.findByRole("button", { name: "Import" }));

        await expect(screen.findByText(/row 2 is not valid CSV/)).resolves.toBeInTheDocument();
        const deleted = fetchMock.mock.calls[2];
        expect([new URL(String(deleted[0])).pathname, deleted[1]?.method]).toEqual([`${BASE}/datasets/ds-3`, "DELETE"]);
        expect(onImported).not.toHaveBeenCalled();
    });

    test("never deletes the existing dataset it imports into, however the file is refused", async () => {
        const json = (status: number, body?: unknown) => new Response(body === undefined ? null : JSON.stringify(body), { status });
        for (const [status, body] of [
            [422, { message: "Unable to import examples", errorId: "e1", validationDetails: { file: ["row 2 is not valid CSV"] } }],
            [413, { message: "Request body too large", errorId: "e2" }],
        ] as const) {
            fetchMock.mockReset();
            fetchMock.mockResolvedValueOnce(json(status, body));
            const onImported = vi.fn();

            const { unmount } = renderWithProviders(<DatasetImportDialog open onClose={vi.fn()} onImported={onImported} datasetId="ds-1" currentExampleCount={3} />);
            fireEvent.change(document.querySelector('input[name="dataset-file"]') as HTMLInputElement, { target: { files: [csvFile("golden.csv")] } });
            fireEvent.click(await screen.findByRole("button", { name: "Import" }));

            await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
            await waitFor(() => expect(screen.getByRole("button", { name: "Import" })).toBeEnabled());
            expect(onImported).not.toHaveBeenCalled();
            const methods = fetchMock.mock.calls.map(c => `${c[1]?.method} ${new URL(String(c[0])).pathname}`);
            expect(methods).toEqual([`PUT ${BASE}/datasets/ds-1/examples/import`]);
            unmount();
        }
    });

    test("keeps the dataset when the import never got an answer", async () => {
        fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ data: dataset("ds-4", "golden") }), { status: 201 })).mockRejectedValueOnce(new TypeError("Failed to fetch"));

        renderWithProviders(<DatasetImportDialog open onClose={vi.fn()} onImported={vi.fn()} />);
        fireEvent.change(document.querySelector('input[name="dataset-file"]') as HTMLInputElement, { target: { files: [csvFile("golden.csv")] } });
        fireEvent.click(await screen.findByRole("button", { name: "Import" }));

        await expect(screen.findByText("Failed to fetch")).resolves.toBeInTheDocument();
        expect(fetchMock).toHaveBeenCalledTimes(2);
    });

    test("imports into the dataset it could not delete when the same file is tried again", async () => {
        const json = (status: number, body?: unknown) => new Response(body === undefined ? null : JSON.stringify(body), { status });
        vi.spyOn(console, "warn").mockImplementation(() => {});
        fetchMock
            .mockResolvedValueOnce(json(201, { data: dataset("ds-5", "golden") }))
            .mockResolvedValueOnce(json(422, { message: "Unable to import examples", errorId: "e1", validationDetails: { file: ["row 2 is not valid CSV"] } }))
            .mockResolvedValueOnce(json(500, { message: "Unable to delete dataset", errorId: "e2" }))
            .mockResolvedValueOnce(json(200, { data: { created: 1, skipped: 0, dropped: 0, limit: 5000 } }));
        const onImported = vi.fn();

        renderWithProviders(<DatasetImportDialog open onClose={vi.fn()} onImported={onImported} />);
        fireEvent.change(document.querySelector('input[name="dataset-file"]') as HTMLInputElement, { target: { files: [csvFile("golden.csv")] } });
        fireEvent.click(await screen.findByRole("button", { name: "Import" }));
        await screen.findByText(/row 2 is not valid CSV/);
        fireEvent.click(screen.getByRole("button", { name: "Import" }));

        await waitFor(() => expect(onImported).toHaveBeenCalledWith("ds-5", "golden"));
        const retried = fetchMock.mock.calls[3];
        expect([new URL(String(retried[0])).pathname, retried[1]?.method]).toEqual([`${BASE}/datasets/ds-5/examples/import`, "PUT"]);
        expect(fetchMock).toHaveBeenCalledTimes(4);
    });
});
