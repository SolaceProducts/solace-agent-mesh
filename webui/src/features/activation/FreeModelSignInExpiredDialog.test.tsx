// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { type Activation, activationKeys, type FreeModelStatus } from "@/api/activation";
import * as service from "@/api/activation/service";
import { useModelConfigs } from "@/api/models";
import type { ModelConfig } from "@/api/models/types";

import { queryClient } from "@/providers/query/QueryClient";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { FreeModelSignInExpiredDialog } from "./FreeModelSignInExpiredDialog";

vi.mock("@/api/activation/service", async importOriginal => ({
    ...(await importOriginal<typeof import("@/api/activation/service")>()),
    getActivation: vi.fn(),
    getFreeModel: vi.fn(),
    signInFreeModel: vi.fn(),
    cancelSignIn: vi.fn(),
}));

vi.mock("@/api/models", async importOriginal => ({
    ...(await importOriginal<typeof import("@/api/models")>()),
    useModelConfigs: vi.fn(),
}));

const freeModel = { grantId: "1c58a105-c275-4401-b9af-9b1af81a80af", provider: "openai", apiBase: "https://llm.example.test/v1", generalModelName: "free", planningModelName: "free-planning" };
const onFreeModel: Activation = { state: "active", signingIn: false, agreements: { eulaClickwrapId: "eula-1" }, freeModelAvailable: true, freeModel, freeModelChosen: true };
const freeRow = (alias: string) =>
    ({ id: `${alias}-id`, alias, provider: "openai", modelName: alias === "planning" ? "free-planning" : "free", apiBase: freeModel.apiBase, authConfig: { type: "oauth2_delegated", grant_id: freeModel.grantId } }) as unknown as ModelConfig;

const expired: FreeModelStatus = { status: "reauthorization_required", remainingPercent: null, models: [] };
const available: FreeModelStatus = { status: "available", remainingPercent: 60, models: [] };

const promptTitle = "Sign In Again to Keep Using Free Credits";

function renderDialog() {
    return renderWithProviders(<FreeModelSignInExpiredDialog />, { configContextValues: { activationGateEnabled: true } });
}

describe("the free model's sign-in expiry dialog", () => {
    beforeEach(() => {
        vi.mocked(service.getActivation).mockResolvedValue(onFreeModel);
        vi.mocked(useModelConfigs).mockReturnValue({ data: [freeRow("general"), freeRow("planning")], isLoading: false, error: null } as unknown as ReturnType<typeof useModelConfigs>);
        vi.mocked(service.getFreeModel).mockResolvedValue(expired);
    });
    afterEach(() => vi.resetAllMocks());

    test("opens once the credits read says the sign-in expired", async () => {
        renderDialog();

        expect(await screen.findByRole("dialog", { name: promptTitle })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Sign In Again" })).toBeEnabled();
    });

    test("stays closed while the credits are available", async () => {
        vi.mocked(service.getFreeModel).mockResolvedValue(available);
        renderDialog();

        await waitFor(() => expect(service.getFreeModel).toHaveBeenCalled());
        expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });

    test("Not Now keeps it closed while the sign-in stays expired", async () => {
        renderDialog();
        await userEvent.click(await screen.findByRole("button", { name: "Not Now" }));
        expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

        const readsBefore = vi.mocked(service.getFreeModel).mock.calls.length;
        await act(() => queryClient.refetchQueries({ queryKey: activationKeys.freeModel() }));
        await waitFor(() => expect(vi.mocked(service.getFreeModel).mock.calls.length).toBeGreaterThan(readsBefore));
        expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });

    test("signing in from the dialog closes it and reads the credits again", async () => {
        vi.mocked(service.signInFreeModel).mockResolvedValue({ ...onFreeModel, signingIn: true });
        renderDialog();
        const signIn = await screen.findByRole("button", { name: "Sign In Again" });
        vi.mocked(service.getFreeModel).mockResolvedValue(available);
        const readsBefore = vi.mocked(service.getFreeModel).mock.calls.length;

        await userEvent.click(signIn);

        await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument(), { timeout: 5_000 });
        expect(vi.mocked(service.getFreeModel).mock.calls.length).toBeGreaterThan(readsBefore);
    }, 15_000);
});
