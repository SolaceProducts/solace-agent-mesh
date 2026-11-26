// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, renderHook, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import type { ReactNode } from "react";
import { afterAll, afterEach, beforeAll, describe, expect, test, vi } from "vitest";

import { anthropicModelConfig, typesafeDecisionModelConfig } from "@/testing/data/models";
import { toModelV2 } from "@/testing/data/modelsV2";
import { paginatedResponse } from "@/testing/helpers/handlers";
import { StoryProvider } from "@/testing/providers/StoryProvider";

import { useDefaultModelSelection } from "../hooks/useDefaultModelSelection";

import { ModelSelect } from "./ModelSelect";

const server = setupServer();

beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function serveModels() {
    const data = [typesafeDecisionModelConfig, anthropicModelConfig];
    server.use(http.get("*/api/v2/platform/models", () => HttpResponse.json(paginatedResponse(data.map(toModelV2)))));
}

const wrapper = ({ children }: { children: ReactNode }) => <StoryProvider>{children}</StoryProvider>;

describe("evaluation model pickers and decision models", () => {
    test("the model select never offers a decision model", async () => {
        serveModels();
        const { container } = render(<ModelSelect value="" onChange={vi.fn()} />, { wrapper });
        const user = userEvent.setup();

        await waitFor(() => expect(screen.queryByText("Loading...")).toBeNull());
        await user.click(container.querySelector("svg.lucide-chevron-down")!.parentElement as HTMLElement);

        expect(await screen.findByText(anthropicModelConfig.alias)).toBeInTheDocument();
        expect(screen.queryByText(typesafeDecisionModelConfig.alias)).toBeNull();
    });

    test("the default selection skips a decision model listed first", async () => {
        serveModels();
        const setModel = vi.fn();
        renderHook(() => useDefaultModelSelection(true, undefined, setModel), { wrapper });

        await waitFor(() => expect(setModel).toHaveBeenCalled());
        expect(setModel).toHaveBeenCalledWith(anthropicModelConfig.id);
        expect(setModel).not.toHaveBeenCalledWith(typesafeDecisionModelConfig.id);
    });
});
