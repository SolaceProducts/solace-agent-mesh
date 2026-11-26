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
import { buildModelPayload, CHAT_PROVIDERS, type ModelProvider } from "./modelProviderUtils";

expect.extend(matchers);

const ALL_CAPABILITIES = { userHasCapability: () => true, CAPABILITIES };

function pricedModel(): ModelConfig {
    return {
        ...mockModelConfigs[0],
        id: "priced-model",
        alias: "general",
        pricing: { input: "3", output: "15", cacheRead: null, cacheWrite: null },
    };
}

function renderEdit(availableProviders: ModelProvider[] = []) {
    return renderWithProviders(<ModelEdit isNew={false} modelToEdit={pricedModel()} onSave={vi.fn()} availableProviders={availableProviders} />, {
        userContextValues: ALL_CAPABILITIES,
    });
}

describe("ModelEdit pricing section", () => {
    test("loads the stored rates into the fields", () => {
        renderEdit();

        expect(screen.getByLabelText(/Input Price/)).toHaveValue("3.00");
        expect(screen.getByLabelText(/Output Price/)).toHaveValue("15.00");
    });

    test("clears the rates when the provider changes", async () => {
        const onSave = vi.fn().mockResolvedValue(undefined);
        renderWithProviders(
            <ModelEdit
                isNew={false}
                modelToEdit={{ ...pricedModel(), pricing: { input: "3", output: "15", cacheRead: "0.3", cacheWrite: "3.75" } }}
                onSave={onSave}
                availableProviders={CHAT_PROVIDERS}
                modelsByProvider={{ openai: [{ id: "gpt-4o", label: "gpt-4o" }] }}
            />,
            { userContextValues: ALL_CAPABILITIES }
        );
        const user = userEvent.setup();

        await user.click(screen.getAllByRole("combobox")[0]);
        await user.click(await screen.findByText("OpenAI"));
        await user.type(document.querySelector('input[name="apiKey"]') as HTMLInputElement, "sk-test");
        await user.click(await screen.findByPlaceholderText(/Type or select a model/i));
        await user.click(await screen.findByText("gpt-4o"));
        expect(screen.getByLabelText(/Input Price/)).toHaveValue("");
        expect(screen.getByLabelText(/Output Price/)).toHaveValue("");
        expect(screen.queryByTestId("model-pricing-cache-rates")).not.toBeInTheDocument();
        fireEvent.submit(document.getElementById("model-form") as HTMLFormElement);

        await waitFor(() => expect(onSave).toHaveBeenCalled());
        expect(buildModelPayload(onSave.mock.calls[0][0]).pricing).toEqual({ input: null, output: null, cacheRead: null, cacheWrite: null });
    });
});
