// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { OpenFeature, OpenFeatureProvider } from "@openfeature/react-sdk";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterAll, beforeAll, describe, expect, test, vi } from "vitest";

import { CAPABILITIES, UserContext, type UserContextValue } from "@/providers/contexts/UserContext";
import { SamFeatureProvider } from "@/providers/openfeature/SamFeatureProvider";

import type { PromptGroup } from "@/types/prompts";

import { MockConfigProvider } from "@/testing/providers/MockConfigProvider";

import { PromptCard } from "./PromptCard";

const userCtx: UserContextValue = {
    userHasCapability: () => false,
    CAPABILITIES,
    capabilitiesLoaded: true,
    scopes: [],
    hasScope: () => false,
    isSuperAdmin: false,
};

beforeAll(async () => {
    await OpenFeature.setProviderAndWait(new SamFeatureProvider({ prompt_sharing: true, prompt_version_history: false }));
});

afterAll(async () => {
    await OpenFeature.setProviderAndWait(new SamFeatureProvider({}));
});

const basePrompt: PromptGroup = {
    id: "p-1",
    name: "Demo Prompt",
    userId: "owner@example.com",
    isShared: false,
    isPinned: false,
    createdAt: "2026-05-29T08:00:00Z",
    updatedAt: "2026-05-29T08:00:00Z",
};

function renderCard(promptOverrides: Partial<PromptGroup> = {}) {
    const prompt = { ...basePrompt, ...promptOverrides };
    render(
        <OpenFeatureProvider>
            <MockConfigProvider mockValues={{ identityServiceType: "okta" }}>
                <UserContext.Provider value={userCtx}>
                    <PromptCard prompt={prompt} isSelected={false} onPromptClick={vi.fn()} onEdit={vi.fn()} onDelete={vi.fn()} />
                </UserContext.Provider>
            </MockConfigProvider>
        </OpenFeatureProvider>
    );
    return prompt;
}

describe("PromptCard access icon", () => {
    test("owner: renders the owner icon with descriptive aria-label", () => {
        renderCard({ accessLevel: "owner" });
        expect(screen.getByLabelText(/owner of this prompt/i)).toBeInTheDocument();
    });

    test("editor: renders the editor icon with descriptive aria-label", () => {
        renderCard({ accessLevel: "resource_editor" });
        expect(screen.getByLabelText(/editor.*this prompt/i)).toBeInTheDocument();
    });

    test("viewer: renders the viewer icon with descriptive aria-label", () => {
        renderCard({ accessLevel: "resource_viewer" });
        expect(screen.getByLabelText(/viewer.*this prompt/i)).toBeInTheDocument();
    });

    test("missing accessLevel: no icon rendered", () => {
        renderCard();
        expect(screen.queryByTestId("prompt-access-icon")).not.toBeInTheDocument();
    });
});

describe("PromptCard version-history gating (flag off)", () => {
    test("owner delete action reads 'Delete', not 'Delete All Versions'", async () => {
        const user = userEvent.setup();
        renderCard({ accessLevel: "owner" });

        await user.click(screen.getByTestId("Actions"));

        expect(screen.getByRole("menuitem", { name: /^delete$/i })).toBeInTheDocument();
        expect(screen.queryByRole("menuitem", { name: /delete all versions/i })).not.toBeInTheDocument();
    });

    test("no 'Active Version' line even when the prompt carries a version", () => {
        renderCard({ accessLevel: "owner", productionPrompt: { version: 3 } as PromptGroup["productionPrompt"] });
        expect(screen.queryByText(/active version/i)).not.toBeInTheDocument();
    });
});
