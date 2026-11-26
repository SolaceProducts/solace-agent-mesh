// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useQueryClient } from "@tanstack/react-query";
import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { artifactKeys } from "@/api/artifacts";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import { SeedSessionArtifacts } from "@/testing/helpers/sessionArtifacts";
import type { ArtifactInfo } from "@/types";

import { ChatMessageDataProvider } from "./ChatMessageDataProvider";
import { useChatMessageData } from "./contexts";

const SESSION_ID = "session-cards";
const file = (filename: string, tags?: string[]): ArtifactInfo => ({ filename, mimeType: "text/plain", size: 1, lastModified: "2026-09-25T00:00:00Z", uri: `artifact://${SESSION_ID}/${filename}`, tags });
const deck = file("deck.md");
const spec = file("deck-spec.yaml", ["__working"]);

function SeedFullList({ artifacts }: { artifacts: ArtifactInfo[] }) {
    useQueryClient().setQueryData(artifactKeys.sessionWithWorking(SESSION_ID), artifacts);
    return null;
}

function ListedNames() {
    const { allArtifacts } = useChatMessageData();
    return <div data-testid="names">{allArtifacts.map(artifact => artifact.filename).join(",")}</div>;
}

describe("ChatMessageDataProvider artifacts", () => {
    it("include the working files the shared list strips", () => {
        renderWithProviders(
            <>
                <SeedSessionArtifacts sessionId={SESSION_ID} artifacts={[deck]} />
                <SeedFullList artifacts={[deck, spec]} />
                <ChatMessageDataProvider>
                    <ListedNames />
                </ChatMessageDataProvider>
            </>,
            { chatContextValues: { sessionId: SESSION_ID, isSharedSession: true } }
        );

        expect(screen.getByTestId("names")).toHaveTextContent("deck.md,deck-spec.yaml");
    });
});
