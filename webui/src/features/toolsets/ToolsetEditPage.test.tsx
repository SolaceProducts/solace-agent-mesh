// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import type { ChatContextValue } from "@/providers/contexts";

import { mockCustomToolsets } from "@/testing/data";
import { StoryProvider } from "@/testing/providers/StoryProvider";

import { ToolsetEditPage } from "./ToolsetEditPage";

class FakeDataTransfer {
    private readonly _files: File[] = [];
    items = { add: (f: File) => this._files.push(f) };
    get files(): FileList {
        return this._files as unknown as FileList;
    }
}

const existing = { ...mockCustomToolsets[0], agents: [] };

const toolsetV2 = {
    ...existing,
    type: "toolset",
    origin: "custom",
    runtime: null,
    discoveryStatus: "ready",
    discoveryErrors: [],
    bundle: null,
    tools: [],
    configSummary: null,
    capabilities: {},
    default: false,
    aliases: [],
    createdBy: null,
    updatedBy: null,
    createdTime: null,
    updatedTime: null,
};

const emptyPage = { data: [], meta: { pagination: { pageNumber: 1, count: 0, pageSize: 100, nextPage: null, totalPages: 1 } } };

const json = (status: number, body: unknown) => Promise.resolve(new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } }));

type Reply = (method: string, url: string) => Promise<Response> | undefined;

const fetchMock = vi.fn<typeof fetch>();
let reply: Reply = () => undefined;
const calls = () => fetchMock.mock.calls.map(([input, init]) => `${init?.method ?? "GET"} ${new URL(String(input), "http://localhost").pathname}`);

const savedResource: Reply = method => (method === "POST" ? json(201, { data: toolsetV2 }) : method === "PUT" || method === "PATCH" ? json(200, { data: toolsetV2 }) : undefined);

function renderPage(isNew: boolean, chat: Partial<ChatContextValue> = {}, state?: unknown) {
    const chatContextValues: Partial<ChatContextValue> = {
        customToolsetToEdit: isNew ? null : existing,
        setCustomToolsetToEdit: vi.fn(),
        setNewlyCreatedToolsetId: vi.fn(),
        navigate: vi.fn(),
        addNotification: vi.fn(),
        useLoaderData: (() => ({ isNew, toolsetId: isNew ? undefined : existing.id })) as ChatContextValue["useLoaderData"],
        ...chat,
    };
    const router = createMemoryRouter(
        [
            {
                path: "*",
                element: (
                    <StoryProvider chatContextValues={chatContextValues} userContextValues={{ userHasCapability: () => true }}>
                        <ToolsetEditPage />
                    </StoryProvider>
                ),
            },
        ],
        { initialEntries: [{ pathname: "/toolsets/edit", state }] }
    );
    render(<RouterProvider router={router} />);
    return chatContextValues;
}

describe("ToolsetEditPage on /api/v2", () => {
    const originalDataTransfer = globalThis.DataTransfer;

    beforeAll(() => {
        Element.prototype.scrollIntoView = vi.fn();
        globalThis.DataTransfer = FakeDataTransfer as unknown as typeof DataTransfer;
    });
    afterAll(() => {
        globalThis.DataTransfer = originalDataTransfer;
    });
    beforeEach(() => {
        fetchMock.mockImplementation((input, init) => {
            const url = String(input);
            const method = init?.method ?? "GET";
            return reply(method, url) ?? (method === "GET" ? json(200, emptyPage) : json(500, { message: "unexpected call", errorId: "e0" }));
        });
        vi.stubGlobal("fetch", fetchMock);
    });
    afterEach(() => {
        fetchMock.mockReset();
        reply = () => undefined;
        vi.unstubAllGlobals();
        vi.restoreAllMocks();
    });

    it("creates the toolset and uploads its bundle through /api/v2", async () => {
        reply = savedResource;
        const droppedFile = new File(["PK"], "order_tools.zip", { type: "application/zip" });
        const chat = renderPage(true, {}, { droppedFile });
        await screen.findByTestId("toolset-name");

        await userEvent.type(screen.getByTestId("toolset-name"), "order_tools");
        await userEvent.type(screen.getByTestId("toolset-description"), "Looks up customer orders.");
        await userEvent.click(screen.getByTestId("toolsetSaveButton"));

        await waitFor(() => expect(chat.navigate).toHaveBeenCalledWith("/toolsets"));
        expect(calls()).toEqual(expect.arrayContaining(["POST /api/v2/platform/toolsets", `PUT /api/v2/platform/toolsets/${existing.id}/bundle`]));
        expect(calls().filter(call => call.includes("/api/v1/"))).toEqual([]);
    });

    it("uploads a re-imported bundle and updates the toolset through /api/v2", async () => {
        reply = savedResource;
        const chat = renderPage(false);

        await screen.findByTestId("toolset-name");
        const reimport = document.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!;
        fireEvent.change(reimport, { target: { files: [new File(["PK"], "order_tools.zip", { type: "application/zip" })] } });
        await userEvent.click(screen.getByTestId("toolsetSaveButton"));

        await waitFor(() => expect(chat.navigate).toHaveBeenCalledWith(`/toolsets/${existing.id}`));
        const writes = calls().filter(call => !call.startsWith("GET "));
        expect(writes).toEqual([`PATCH /api/v2/platform/toolsets/${existing.id}`, `PUT /api/v2/platform/toolsets/${existing.id}/bundle`]);
        expect(calls().filter(call => call.includes("/api/v1/"))).toEqual([]);
    });

    it("keeps the bundle when the platform refuses a rename sent with a re-import", async () => {
        reply = method => (method === "PATCH" ? json(422, { message: "Validation failed", errorId: "e4", validationDetails: { name: ["rename unsupported after upload"] } }) : undefined);
        renderPage(false);

        await screen.findByTestId("toolset-name");
        await userEvent.clear(screen.getByTestId("toolset-name"));
        await userEvent.type(screen.getByTestId("toolset-name"), "order_tools_renamed");
        const reimport = document.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!;
        fireEvent.change(reimport, { target: { files: [new File(["PK"], "order_tools.zip", { type: "application/zip" })] } });
        await userEvent.click(screen.getByTestId("toolsetSaveButton"));

        expect(await screen.findByText("rename unsupported after upload")).toBeInTheDocument();
        expect(calls().filter(call => call.includes("/bundle"))).toEqual([]);
    });

    it("shows a v2 field error on the input it names, not in the error dialog", async () => {
        reply = method => (method === "PATCH" ? json(422, { message: "Validation failed", errorId: "e1", validationDetails: { name: ["rename unsupported after upload"] } }) : undefined);
        const chat = renderPage(false);

        await screen.findByTestId("toolset-name");
        await userEvent.click(screen.getByTestId("toolsetSaveButton"));

        expect(await screen.findByText("rename unsupported after upload")).toBeInTheDocument();
        expect(screen.getByTestId("toolset-name")).toHaveAttribute("aria-invalid", "true");
        expect(screen.queryByText("Toolset Update Failed")).not.toBeInTheDocument();
        expect(chat.navigate).not.toHaveBeenCalled();
    });

    it("shows a create's v2 field error on its input too", async () => {
        reply = method => (method === "POST" ? json(422, { message: "Validation failed", errorId: "e2", validationDetails: { description: ["must not contain markup"] } }) : undefined);
        renderPage(true, {}, { droppedFile: new File(["PK"], "order_tools.zip", { type: "application/zip" }) });
        await screen.findByTestId("toolset-name");

        await userEvent.type(screen.getByTestId("toolset-name"), "order_tools");
        await userEvent.type(screen.getByTestId("toolset-description"), "Looks up customer orders.");
        await userEvent.click(screen.getByTestId("toolsetSaveButton"));

        expect(await screen.findByText("must not contain markup")).toBeInTheDocument();
        expect(screen.queryByText("Toolset Creation Failed")).not.toBeInTheDocument();
    });

    it("keeps an error for a field the form has no input for in the dialog", async () => {
        reply = method => (method === "PATCH" ? json(422, { message: "Validation failed", errorId: "e3", validationDetails: { name: ["is reserved"], runtime: ["is not supported"] } }) : undefined);
        renderPage(false);

        await screen.findByTestId("toolset-name");
        await userEvent.click(screen.getByTestId("toolsetSaveButton"));

        expect(await screen.findByText("Toolset Update Failed")).toBeInTheDocument();
        expect(screen.getByText("is reserved")).toBeInTheDocument();
        const dialog = screen.getByRole("dialog");
        expect(dialog).toHaveTextContent("runtime: is not supported");
        expect(dialog).not.toHaveTextContent("is reserved");
    });

    it("shows the rename a failed re-import left behind", async () => {
        reply = method => (method === "PATCH" ? json(200, { data: { ...toolsetV2, name: "order_tools_renamed" } }) : method === "PUT" ? json(500, { message: "bundle store unavailable", errorId: "e5" }) : undefined);
        const refetches = vi.fn();
        const chat = renderPage(false);
        fetchMock.mockImplementation((input, init) => {
            const method = init?.method ?? "GET";
            if (method === "GET" && String(input).includes("/platform/toolsets")) refetches();
            return reply(method, String(input)) ?? (method === "GET" ? json(200, emptyPage) : json(500, { message: "unexpected call", errorId: "e0" }));
        });

        await screen.findByTestId("toolset-name");
        const listReadsBeforeSave = refetches.mock.calls.length;
        const reimport = document.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!;
        fireEvent.change(reimport, { target: { files: [new File(["PK"], "order_tools.zip", { type: "application/zip" })] } });
        await userEvent.click(screen.getByTestId("toolsetSaveButton"));

        expect(await screen.findByText("Toolset Update Failed")).toBeInTheDocument();
        expect(refetches.mock.calls.length).toBeGreaterThan(listReadsBeforeSave);
        expect(chat.setCustomToolsetToEdit).toHaveBeenCalledWith(null);
        expect(chat.navigate).not.toHaveBeenCalled();
    });
});
