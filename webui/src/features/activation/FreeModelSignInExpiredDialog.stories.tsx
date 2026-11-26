// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, screen, userEvent, within } from "storybook/test";

import type { Activation } from "@/api/activation";

import { paginatedResponse, withDefaults } from "@/testing/helpers";

import { FreeModelSignInExpiredDialog } from "./FreeModelSignInExpiredDialog";

const freeModel = { grantId: "grant-1", provider: "openai", apiBase: "https://llm.example.test/v1", generalModelName: "free", planningModelName: "free-planning" };
const onFreeModel: Activation = { state: "active", signingIn: false, agreements: { eulaClickwrapId: "eula-1" }, freeModelAvailable: true, freeModel, freeModelChosen: true };
const expired = { status: "reauthorization_required", remainingPercent: null, models: [] };
const available = { status: "available", remainingPercent: 89, models: [] };

function freeModelRow(alias: string, modelName: string) {
    return {
        id: `model-${alias}`,
        type: "model",
        alias,
        provider: freeModel.provider,
        modelName,
        apiBase: freeModel.apiBase,
        authType: "oauth2_delegated",
        authConfig: { type: "oauth2_delegated", grant_id: freeModel.grantId },
        modelParams: {},
        modelType: "chat",
        description: null,
        maxInputTokens: null,
        pricing: null,
        createdBy: null,
        updatedBy: null,
        createdTime: null,
        updatedTime: null,
    };
}

function handlers({ browserOpen = false } = {}) {
    let signedIn = false;
    return withDefaults(
        http.get("*/api/v1/activation", () => HttpResponse.json({ ...onFreeModel, signingIn: browserOpen })),
        http.post(/\/api\/v1\/freeModel:signIn$/, () => {
            signedIn = !browserOpen;
            return HttpResponse.json({ ...onFreeModel, signingIn: true });
        }),
        http.get("*/api/v1/freeModel", () => HttpResponse.json(signedIn ? available : expired)),
        http.get("*/api/v2/platform/models", () => HttpResponse.json(paginatedResponse([freeModelRow("general", "free"), freeModelRow("planning", "free-planning")])))
    );
}

const meta = {
    title: "Activation/FreeModelSignInExpiredDialog",
    component: FreeModelSignInExpiredDialog,
    parameters: {
        configContext: { activationGateEnabled: true },
        msw: { handlers: handlers() },
        docs: { description: { component: "Asks the person to sign in again once the free model's Solace ID sign-in has expired, and completes the sign-in in place." } },
    },
} satisfies Meta<typeof FreeModelSignInExpiredDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Prompt: Story = {
    play: async () => {
        const dialog = await screen.findByRole("dialog", { name: "Sign In Again to Keep Using Free Credits" });
        await expect(within(dialog).getByRole("button", { name: "Sign In Again" })).toBeEnabled();
    },
};

export const WaitingForTheBrowser: Story = {
    parameters: { msw: { handlers: handlers({ browserOpen: true }) } },
    play: async () => {
        const dialog = await screen.findByRole("dialog");
        await userEvent.click(within(dialog).getByRole("button", { name: "Sign In Again" }));
        await expect(await within(dialog).findByRole("status")).toHaveTextContent("Finish signing in with Solace ID in your browser.");
    },
};
