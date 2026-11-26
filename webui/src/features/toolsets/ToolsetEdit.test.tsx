// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, waitFor } from "@testing-library/react";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { Toolset } from "@/types";

import { ToolsetEdit } from "./ToolsetEdit";

vi.mock("./ToolsetToolCards", () => ({
    ToolsetToolCards: ({ onConfigSave }: { onConfigSave?: (toolName: string, values: Record<string, unknown>) => void }) => (
        <button type="button" onClick={() => onConfigSave?.("lookup", { region: "us" })}>
            save lookup config
        </button>
    ),
}));

class FakeDataTransfer {
    private readonly _files: File[] = [];
    items = { add: (f: File) => this._files.push(f) };
    get files(): FileList {
        return this._files as unknown as FileList;
    }
}

const emptyEnvelope = () => {
    const body = JSON.stringify({ data: [], meta: { pagination: { pageNumber: 1, count: 0, pageSize: 0, nextPage: null, totalPages: 1 } } });
    return {
        ok: true,
        status: 200,
        text: () => Promise.resolve(body),
        json: () => Promise.resolve(JSON.parse(body)),
        headers: new Headers({ "content-type": "application/json" }),
    };
};

const withCapability = { userContextValues: { userHasCapability: () => true } };

describe("ToolsetEdit initial file seeding", () => {
    const originalDataTransfer = globalThis.DataTransfer;
    beforeAll(() => {
        Element.prototype.scrollIntoView = vi.fn();
        Element.prototype.hasPointerCapture = vi.fn(() => false);
        Element.prototype.releasePointerCapture = vi.fn();
        globalThis.DataTransfer = FakeDataTransfer as unknown as typeof DataTransfer;
    });
    afterAll(() => {
        globalThis.DataTransfer = originalDataTransfer;
    });
    beforeEach(() => {
        vi.stubGlobal(
            "fetch",
            vi.fn().mockImplementation(() => Promise.resolve(emptyEnvelope()))
        );
    });
    afterEach(() => {
        vi.unstubAllGlobals();
        vi.clearAllMocks();
    });

    it("preloads the create form's file field from a dropped file", async () => {
        const droppedFile = new File(["x"], "dropped-toolset.zip", { type: "application/zip" });
        const { findByText } = renderWithProviders(<ToolsetEdit isNew initialFile={droppedFile} onSubmit={vi.fn()} />, { ...withCapability });

        expect(await findByText("dropped-toolset.zip")).toBeInTheDocument();
    });

    it("shows no preloaded file when none was dropped", async () => {
        const { findByText, queryByText } = renderWithProviders(<ToolsetEdit isNew onSubmit={vi.fn()} />, { ...withCapability });

        await findByText("Tools");
        expect(queryByText("dropped-toolset.zip")).not.toBeInTheDocument();
    });
});

describe("ToolsetEdit configuration on /api/v2", () => {
    const readyToolset: Toolset = {
        id: "ts-1",
        name: "order_tools",
        description: "Orders.",
        type: "custom",
        discoveryStatus: "ready",
        discoveryErrors: [],
        zipFilename: "order_tools.zip",
        zipFileSize: 127,
        files: [],
        tools: [],
        createdBy: "u1",
        updatedBy: "u1",
        createdAt: "2026-01-02T03:04:05.678Z",
        updatedAt: "2026-01-02T03:04:05.678Z",
    };
    const json = (body: unknown) => Promise.resolve(new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } }));
    const fetchMock = vi.fn((input: RequestInfo | URL, _init?: RequestInit) => {
        const url = String(input);
        if (url.includes("/api/v2/platform/toolsets/ts-1/config")) return json({ data: { values: { lookup: { region: "eu" } }, updatedBy: null, updatedTime: null } });
        return Promise.resolve(emptyEnvelope() as unknown as Response);
    });
    const urls = () => fetchMock.mock.calls.map(([input]) => String(input));

    beforeAll(() => {
        Element.prototype.scrollIntoView = vi.fn();
    });
    beforeEach(() => {
        vi.stubGlobal("fetch", fetchMock);
    });
    afterEach(() => {
        fetchMock.mockClear();
        vi.unstubAllGlobals();
    });

    it("loads a ready toolset's configuration from /api/v2", async () => {
        renderWithProviders(<ToolsetEdit isNew={false} initialData={readyToolset} onSubmit={vi.fn()} />, withCapability);

        await waitFor(() => expect(urls().some(u => u.includes("/api/v2/platform/toolsets/ts-1/config"))).toBe(true));
        expect(urls().some(u => u.includes("/api/v2/platform/toolsets?origin="))).toBe(true);
    });

    it("saves a tool's configuration through /api/v2", async () => {
        const onConfigSuccess = vi.fn();
        const { findByText } = renderWithProviders(<ToolsetEdit isNew={false} initialData={readyToolset} onSubmit={vi.fn()} onConfigSuccess={onConfigSuccess} />, withCapability);

        fireEvent.click(await findByText("save lookup config"));

        await waitFor(() => expect(onConfigSuccess).toHaveBeenCalledWith("Configuration saved"));
        const patch = fetchMock.mock.calls.find(([, init]) => init?.method === "PATCH");
        expect(String(patch?.[0])).toContain("/api/v2/platform/toolsets/ts-1/config");
        expect(patch?.[1]?.body).toBe(JSON.stringify({ values: { lookup: { region: "us" } } }));
    });

    it("aborts the configuration load when the form unmounts", async () => {
        const { unmount } = renderWithProviders(<ToolsetEdit isNew={false} initialData={readyToolset} onSubmit={vi.fn()} />, withCapability);

        await waitFor(() => expect(urls().some(u => u.includes("/toolsets/ts-1/config"))).toBe(true));
        const [, init] = fetchMock.mock.calls.find(([input]) => String(input).includes("/toolsets/ts-1/config"))!;
        expect(init?.signal?.aborted).toBe(false);
        unmount();
        expect(init?.signal?.aborted).toBe(true);
    });
});
