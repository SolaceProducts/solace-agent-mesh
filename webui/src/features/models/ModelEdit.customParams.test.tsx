// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import * as matchers from "@testing-library/jest-dom/matchers";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import type { ModelConfig } from "@/api/models/types";

import { CAPABILITIES } from "@/providers/contexts/UserContext";

import { mockModelConfigs } from "@/testing/data/models";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { ModelEdit } from "./ModelEdit";
import { buildModelPayload, CHAT_PROVIDERS, type ModelFormData } from "./modelProviderUtils";

expect.extend(matchers);

const ALL_CAPABILITIES = { userHasCapability: () => true, CAPABILITIES };

function modelWithParams(modelParams: Record<string, unknown>): ModelConfig {
    return { ...mockModelConfigs[1], id: "openai-general", modelParams };
}

async function removeParamAndSave(modelParams: Record<string, unknown>, keyToRemove: string): Promise<ReturnType<typeof buildModelPayload>> {
    const onSave = vi.fn().mockResolvedValue(undefined);
    renderWithProviders(<ModelEdit isNew={false} modelToEdit={modelWithParams(modelParams)} onSave={onSave} availableProviders={CHAT_PROVIDERS} />, {
        userContextValues: ALL_CAPABILITIES,
    });
    const user = userEvent.setup();

    await user.click(screen.getByText("Advanced Settings"));
    const keyInput = (await screen.findAllByLabelText(/^Key \d+$/)).find(input => (input as HTMLInputElement).value === keyToRemove);
    expect(keyInput).toBeDefined();
    const row = keyInput!.closest(".grid") as HTMLElement;
    await user.click(row.querySelector("button") as HTMLButtonElement);
    expect(screen.queryByDisplayValue(keyToRemove)).not.toBeInTheDocument();
    fireEvent.submit(document.getElementById("model-form") as HTMLFormElement);

    await waitFor(() => expect(onSave).toHaveBeenCalled());
    const [formData, dirtyFields] = onSave.mock.calls[0] as [ModelFormData, Partial<Record<string, boolean>>];
    return buildModelPayload(formData, dirtyFields);
}

describe("ModelEdit custom parameters", () => {
    test("a removed parameter is left out of the save while the others stay", async () => {
        const payload = await removeParamAndSave({ top_p: 0.9, presence_penalty: 0.5 }, "top_p");

        expect(payload.modelParams).toEqual({ presence_penalty: 0.5, cache_strategy: "5m" });
    });

    test("removing the only parameter leaves none in the save", async () => {
        const payload = await removeParamAndSave({ top_p: 0.9 }, "top_p");

        expect(payload.modelParams).toEqual({ cache_strategy: "5m" });
    });
});
