// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import type { ReactNode } from "react";
import { afterAll, afterEach, beforeAll, describe, expect, test, vi } from "vitest";

import { anthropicModelConfig } from "@/testing/data/models";
import { toModelV2 } from "@/testing/data/modelsV2";
import { paginatedResponse } from "@/testing/helpers/handlers";
import { StoryProvider } from "@/testing/providers/StoryProvider";

import { ModelSelect } from "./ModelSelect";

const server = setupServer();

beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function serveModels() {
    server.use(http.get("*/api/v2/platform/models", () => HttpResponse.json(paginatedResponse([toModelV2(anthropicModelConfig)]))));
}

const wrapper = ({ children }: { children: ReactNode }) => <StoryProvider>{children}</StoryProvider>;

async function renderLoaded(value: string, onChange = vi.fn()) {
    serveModels();
    render(<ModelSelect value={value} onChange={onChange} />, { wrapper });
    await waitFor(() => expect(screen.queryByText("Loading...")).toBeNull());
    return { trigger: screen.getByRole("combobox"), onChange };
}

describe("ModelSelect keyboard access", () => {
    test("tabbing to the trigger and pressing Enter opens the model list", async () => {
        const { trigger } = await renderLoaded("");
        const user = userEvent.setup();

        for (let presses = 0; presses < 5 && document.activeElement !== trigger; presses++) {
            await user.tab();
        }
        expect(trigger).toHaveFocus();
        expect(trigger).toHaveAttribute("aria-expanded", "false");

        await user.keyboard("{Enter}");
        expect(trigger).toHaveAttribute("aria-expanded", "true");
        expect(await screen.findByText(anthropicModelConfig.alias)).toBeInTheDocument();
    });

    test("ArrowDown on the trigger opens the list too", async () => {
        const { trigger } = await renderLoaded("");
        const user = userEvent.setup();

        trigger.focus();
        await user.keyboard("{ArrowDown}");
        expect(trigger).toHaveAttribute("aria-expanded", "true");
    });

    test("a key pressed on the nested Remove button removes the model without opening the list", async () => {
        const { trigger, onChange } = await renderLoaded(anthropicModelConfig.id);
        const user = userEvent.setup();

        screen.getByTitle("Remove").focus();
        await user.keyboard("{Enter}");

        expect(onChange).toHaveBeenCalledWith("");
        expect(trigger).toHaveAttribute("aria-expanded", "false");
    });
});
