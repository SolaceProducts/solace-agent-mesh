// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { ArtifactWithSession } from "@/api/artifacts";
import * as artifactService from "@/api/artifacts/service";

import { CAPABILITIES, UserContext } from "@/providers/contexts/UserContext";

import { AttachArtifactDialog } from "@/features/chat/file/AttachArtifactDialog";

const userContextValue = { userHasCapability: () => true, CAPABILITIES, capabilitiesLoaded: true, scopes: [], hasScope: () => false, isSuperAdmin: false };

const makeArtifact = (overrides: Partial<ArtifactWithSession> = {}): ArtifactWithSession => ({
    filename: "notes.txt",
    size: 100,
    mimeType: "text/plain",
    lastModified: "2026-01-01T00:00:00Z",
    uri: "",
    sessionId: "sess-1",
    sessionName: "Session One",
    ...overrides,
});

function toRawArtifact(a: ArtifactWithSession) {
    return {
        filename: a.filename,
        size: a.size,
        mimeType: a.mimeType,
        lastModified: a.lastModified,
        uri: a.uri,
        version: a.version ?? null,
        sessionId: a.sessionId,
        sessionName: a.sessionName,
        projectId: a.projectId ?? null,
        projectName: a.projectName,
        source: a.source ?? null,
        tags: a.tags ?? null,
    };
}

let getAllArtifactsSpy: ReturnType<typeof vi.spyOn>;
let qc: QueryClient;

function renderDialog(artifacts: ArtifactWithSession[], options: { alreadyAttachedUris?: string[]; onAttach?: (a: ArtifactWithSession[]) => void; onClose?: () => void } = {}) {
    getAllArtifactsSpy.mockResolvedValue({
        artifacts: artifacts.map(toRawArtifact),
        totalCount: artifacts.length,
        hasMore: false,
        nextPage: null,
    });

    qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });

    return render(
        <UserContext.Provider value={userContextValue}>
            <QueryClientProvider client={qc}>
                <AttachArtifactDialog isOpen onClose={options.onClose ?? (() => {})} onAttach={options.onAttach ?? (() => {})} alreadyAttachedUris={options.alreadyAttachedUris} />
            </QueryClientProvider>
        </UserContext.Provider>
    );
}

describe("AttachArtifactDialog", () => {
    beforeEach(() => {
        getAllArtifactsSpy = vi.spyOn(artifactService, "getAllArtifacts");
    });

    afterEach(() => {
        vi.restoreAllMocks();
        qc?.clear();
    });

    test("shows empty state when no artifacts are returned", async () => {
        renderDialog([]);
        expect(await screen.findByText(/no artifacts available/i)).toBeInTheDocument();
    });

    test("hides records that arrive without a canonical URI (treats them as unattachable)", async () => {
        renderDialog([makeArtifact({ filename: "broken.txt", sessionId: "sess-x", uri: "", version: 0 })]);

        expect(await screen.findByText(/no artifacts available/i)).toBeInTheDocument();
        expect(screen.queryByText("broken.txt")).not.toBeInTheDocument();
    });

    test("hides artifacts whose URI is already attached", async () => {
        renderDialog(
            [
                makeArtifact({ filename: "already.txt", sessionId: "sess-1", uri: "artifact://app/user/sess-1/already.txt?version=0" }),
                makeArtifact({ filename: "fresh.txt", sessionId: "sess-1", uri: "artifact://app/user/sess-1/fresh.txt?version=0" }),
            ],
            {
                alreadyAttachedUris: ["artifact://app/user/sess-1/already.txt?version=0"],
            }
        );

        expect(await screen.findByText("fresh.txt")).toBeInTheDocument();
        expect(screen.queryByText("already.txt")).not.toBeInTheDocument();
    });

    test("supports multi-select and emits all chosen artifacts on Attach", async () => {
        const onAttach = vi.fn();
        const onClose = vi.fn();
        renderDialog(
            [
                makeArtifact({ filename: "a.txt", sessionId: "s", uri: "artifact://s/a.txt" }),
                makeArtifact({ filename: "b.txt", sessionId: "s", uri: "artifact://s/b.txt" }),
                makeArtifact({ filename: "c.txt", sessionId: "s", uri: "artifact://s/c.txt" }),
            ],
            { onAttach, onClose }
        );

        await userEvent.click(await screen.findByText("a.txt"));
        await userEvent.click(screen.getByText("c.txt"));

        await userEvent.click(screen.getByRole("button", { name: /attach 2/i }));

        expect(onAttach).toHaveBeenCalledTimes(1);
        const emitted = onAttach.mock.calls[0][0] as ArtifactWithSession[];
        expect(emitted.map(a => a.filename)).toEqual(["a.txt", "c.txt"]);
        expect(onClose).toHaveBeenCalledTimes(1);
    });

    test("Attach button is disabled when nothing is selected", async () => {
        renderDialog([makeArtifact({ uri: "artifact://s/x.txt" })]);

        await screen.findByText("notes.txt");
        const attachBtn = screen.getByRole("button", { name: /^attach$/i });
        expect(attachBtn).toBeDisabled();
    });

    test("Cancel closes without emitting onAttach", async () => {
        const onAttach = vi.fn();
        const onClose = vi.fn();
        renderDialog([makeArtifact({ uri: "artifact://s/x.txt" })], { onAttach, onClose });

        await userEvent.click(await screen.findByText("notes.txt"));
        await userEvent.click(screen.getByRole("button", { name: /cancel/i }));

        expect(onAttach).not.toHaveBeenCalled();
        expect(onClose).toHaveBeenCalled();
    });

    test("search input updates the query value", () => {
        renderDialog([]);

        const search = screen.getByTestId("attach-artifact-search") as HTMLInputElement;
        fireEvent.change(search, { target: { value: "report" } });

        expect(search.value).toBe("report");
    });
});
