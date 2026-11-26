// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect } from "react";
import { act, renderHook, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { useBuilderContext } from "@/providers/contexts/BuilderContext";

import { __resetShowWorkingArtifactsForTest, useShowWorkingArtifacts } from "@/hooks/useShowWorkingArtifacts";

import { SHOW_WORKING_ARTIFACTS_STORAGE_KEY } from "@/features/chat/artifactTags";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { MessageFE } from "@/types";

import { ChatMessage } from "./ChatMessage";

const TASK_ID = "11111111-1111-1111-1111-111111111111";

function agentMessageWith(parts: MessageFE["parts"]): MessageFE {
    return {
        role: "agent",
        isUser: false,
        taskId: TASK_ID,
        isComplete: true,
        parts,
        metadata: { messageId: "m1" },
    } as MessageFE;
}

function seedShowWorkingArtifacts(value: boolean | null): void {
    if (value === null) {
        localStorage.removeItem(SHOW_WORKING_ARTIFACTS_STORAGE_KEY);
    } else {
        localStorage.setItem(SHOW_WORKING_ARTIFACTS_STORAGE_KEY, String(value));
    }
    act(() => __resetShowWorkingArtifactsForTest());
}

function otherTabStorageEvent(init: StorageEventInit): void {
    act(() => {
        globalThis.dispatchEvent(new StorageEvent("storage", init));
    });
}

beforeEach(() => seedShowWorkingArtifacts(null));
afterEach(() => seedShowWorkingArtifacts(null));

const workingSpec = agentMessageWith([
    { kind: "text", text: "Deck ready." },
    { kind: "artifact", name: "deck-spec.yaml", status: "completed", tags: ["__working"], file: { name: "deck-spec.yaml", uri: `artifact://s/deck-spec.yaml` } },
] as MessageFE["parts"]);

describe("ChatMessage __working artifact cards", () => {
    it("hides a __working artifact card while the toggle is off", () => {
        renderWithProviders(<ChatMessage message={workingSpec} isLastWithTaskId />);

        expect(screen.queryByText("deck-spec.yaml")).not.toBeInTheDocument();
        expect(screen.getByText("Deck ready.")).toBeInTheDocument();
    });

    it("shows the card once the toggle is on", () => {
        seedShowWorkingArtifacts(true);

        renderWithProviders(<ChatMessage message={workingSpec} isLastWithTaskId />);

        expect(screen.getByText("deck-spec.yaml")).toBeInTheDocument();
    });

    it("leaves untagged artifact cards visible", () => {
        const deck = agentMessageWith([{ kind: "artifact", name: "presentation.pptx", status: "completed", file: { name: "presentation.pptx", uri: `artifact://s/presentation.pptx` } }] as MessageFE["parts"]);

        renderWithProviders(<ChatMessage message={deck} isLastWithTaskId />);

        expect(screen.getByText("presentation.pptx")).toBeInTheDocument();
    });

    it("renders no bubble when a hidden __working card is the message's only content", () => {
        const loneSpec = agentMessageWith([{ kind: "artifact", name: "notes.md", status: "completed", tags: ["__working"], file: { name: "notes.md", uri: "artifact://s/notes.md" } }] as MessageFE["parts"]);

        const { container } = renderWithProviders(<ChatMessage message={loneSpec} isLastWithTaskId />);

        expect(container.querySelector("[data-task-id] > div")).toBeEmptyDOMElement();
        expect(container).toHaveTextContent(/^$/);
    });

    it("keeps a failed __working save visible so the failure isn't silent", () => {
        const failedSpec = agentMessageWith([{ kind: "artifact", name: "deck-spec.yaml", status: "failed", tags: ["__working"], error: "Failed to create artifact: deck-spec.yaml" }] as MessageFE["parts"]);

        renderWithProviders(<ChatMessage message={failedSpec} isLastWithTaskId />);

        expect(screen.getAllByText(/deck-spec\.yaml/).length).toBeGreaterThan(0);
    });
});

describe("ChatMessage __working artifacts returned with «artifact_return»", () => {
    const returnedSpecAndDeck = agentMessageWith([
        { kind: "text", text: "Deck ready." },
        { kind: "file", file: { name: "solace-overview.pptx", uri: "artifact://s/solace-overview.pptx?version=0" } },
        { kind: "file", file: { name: "deck-spec.yaml", uri: "artifact://s/deck-spec.yaml?version=1" }, metadata: { tags: ["__working"] } },
    ] as MessageFE["parts"]);

    it("hides the returned spec while keeping the returned deck", () => {
        renderWithProviders(<ChatMessage message={returnedSpecAndDeck} isLastWithTaskId />);

        expect(screen.queryByText(/deck-spec\.yaml/)).not.toBeInTheDocument();
        expect(screen.getAllByText(/solace-overview\.pptx/).length).toBeGreaterThan(0);
    });

    it("shows the returned spec once the toggle is on", () => {
        seedShowWorkingArtifacts(true);

        renderWithProviders(<ChatMessage message={returnedSpecAndDeck} isLastWithTaskId />);

        expect(screen.getAllByText(/deck-spec\.yaml/).length).toBeGreaterThan(0);
    });
});

describe("ChatMessage build manifest vs __working: builder claims an auto-tagged manifest before the __working filter", () => {
    function EnableBuilderMode() {
        const { setBuilderMode } = useBuilderContext();
        useEffect(() => setBuilderMode(true), [setBuilderMode]);
        return null;
    }

    function manifestMessage() {
        return agentMessageWith([
            {
                kind: "artifact",
                name: "build_manifest.yaml",
                status: "completed",
                tags: ["__working"],
                file: { name: "build_manifest.yaml", mimeType: "application/vnd.sam-build-manifest+yaml", uri: "artifact://s/build_manifest.yaml" },
            },
        ] as MessageFE["parts"]);
    }

    it("still emits the build-plan part for a __working-tagged build manifest", async () => {
        renderWithProviders(
            <>
                <EnableBuilderMode />
                <ChatMessage message={manifestMessage()} isLastWithTaskId />
            </>
        );

        expect(await screen.findByTestId("build-plan")).toBeInTheDocument();
        expect(screen.queryByText("build_manifest.yaml")).not.toBeInTheDocument();
    });

    it("drops the manifest entirely outside builder mode", async () => {
        renderWithProviders(<ChatMessage message={manifestMessage()} isLastWithTaskId />);

        await waitFor(() => expect(screen.queryByTestId("build-plan")).not.toBeInTheDocument());
        expect(screen.queryByText("build_manifest.yaml")).not.toBeInTheDocument();
    });
});

describe("useShowWorkingArtifacts store sharing", () => {
    it("propagates a toggle from one mounted consumer to an already-mounted ChatMessage", async () => {
        renderWithProviders(<ChatMessage message={workingSpec} isLastWithTaskId />);
        expect(screen.queryByText("deck-spec.yaml")).not.toBeInTheDocument();

        const { result } = renderHook(() => useShowWorkingArtifacts());
        act(() => result.current.toggleShowWorkingArtifacts());

        expect(await screen.findByText("deck-spec.yaml")).toBeInTheDocument();
    });

    it("follows a toggle made in another tab", async () => {
        renderWithProviders(<ChatMessage message={workingSpec} isLastWithTaskId />);
        expect(screen.queryByText("deck-spec.yaml")).not.toBeInTheDocument();

        otherTabStorageEvent({ key: SHOW_WORKING_ARTIFACTS_STORAGE_KEY, newValue: "true" });

        expect(await screen.findByText("deck-spec.yaml")).toBeInTheDocument();
    });

    it("keeps following other tabs after one of several consumers unmounts", async () => {
        const departing = renderHook(() => useShowWorkingArtifacts());
        renderWithProviders(<ChatMessage message={workingSpec} isLastWithTaskId />);
        departing.unmount();

        otherTabStorageEvent({ key: SHOW_WORKING_ARTIFACTS_STORAGE_KEY, newValue: "true" });

        expect(await screen.findByText("deck-spec.yaml")).toBeInTheDocument();
    });

    it("resets to hidden when another tab clears localStorage", async () => {
        seedShowWorkingArtifacts(true);
        renderWithProviders(<ChatMessage message={workingSpec} isLastWithTaskId />);
        expect(screen.getByText("deck-spec.yaml")).toBeInTheDocument();

        otherTabStorageEvent({ key: null });

        await waitFor(() => expect(screen.queryByText("deck-spec.yaml")).not.toBeInTheDocument());
    });
});
