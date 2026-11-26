// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { describe, expect, test } from "vitest";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { ModelEditPage } from "./ModelEditPage";

const REQUEST_URL_TEST_ID = "chatRequestUrl";

function apiBaseInput() {
    return document.querySelector('input[name="apiBase"]') as HTMLInputElement;
}

async function mountAndChooseProvider(user: ReturnType<typeof userEvent.setup>, providerLabel: string) {
    render(
        <StoryProvider>
            <MemoryRouter initialEntries={["/models/new/edit"]}>
                <Routes>
                    <Route path="/models/new/edit" element={<ModelEditPage />} />
                </Routes>
            </MemoryRouter>
        </StoryProvider>
    );
    await screen.findAllByText("Add Model");
    await user.click((await screen.findAllByRole("combobox"))[0]);
    await user.click(await screen.findByText(providerLabel));
    await waitFor(() => expect(apiBaseInput()).toBeTruthy());
}

describe("API Base URL request preview", () => {
    test("custom provider shows the full endpoint as written", async () => {
        const user = userEvent.setup();
        await mountAndChooseProvider(user, "Custom");

        await user.type(apiBaseInput(), "https://ark.cn-beijing.volces.com/api/v3/chat/completions/");

        expect(await screen.findByTestId(REQUEST_URL_TEST_ID)).toHaveTextContent("Requests go to: https://ark.cn-beijing.volces.com/api/v3/chat/completions");
        expect(screen.getByTestId(REQUEST_URL_TEST_ID).textContent).not.toMatch(/\/$/);
    });

    test("custom provider shows the legacy URL for a bare host and follows edits", async () => {
        const user = userEvent.setup();
        await mountAndChooseProvider(user, "Custom");
        expect(screen.queryByTestId(REQUEST_URL_TEST_ID)).not.toBeInTheDocument();

        await user.type(apiBaseInput(), "https://h");
        expect(await screen.findByTestId(REQUEST_URL_TEST_ID)).toHaveTextContent("Requests go to: https://h/v1/chat/completions");

        await user.type(apiBaseInput(), "/v1");
        await waitFor(() => expect(screen.getByTestId(REQUEST_URL_TEST_ID)).toHaveTextContent("Requests go to: https://h/v1/chat/completions"));

        await user.clear(apiBaseInput());
        await waitFor(() => expect(screen.queryByTestId(REQUEST_URL_TEST_ID)).not.toBeInTheDocument());
    });

    test("custom provider explains both forms of the API Base URL", async () => {
        const user = userEvent.setup();
        await mountAndChooseProvider(user, "Custom");

        expect(screen.getByText(/Agent Mesh adds \/v1\/chat\/completions/)).toBeInTheDocument();
    });

    test("another provider with an API base field shows no preview", async () => {
        const user = userEvent.setup();
        await mountAndChooseProvider(user, "Ollama");

        await user.type(apiBaseInput(), "https://h/chat/completions");

        expect(screen.queryByTestId(REQUEST_URL_TEST_ID)).not.toBeInTheDocument();
    });
});
