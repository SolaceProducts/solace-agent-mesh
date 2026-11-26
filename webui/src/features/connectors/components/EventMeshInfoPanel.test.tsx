// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { DOC_LINKS } from "@/utils/docs";

import { EventMeshInfoPanel } from "./EventMeshInfoPanel";

describe("EventMeshInfoPanel", () => {
    test("renders the guidance heading and learn-more link", () => {
        render(<EventMeshInfoPanel />);
        expect(screen.getByText("Define the messages an AI agent can publish")).toBeInTheDocument();
        expect(screen.getByText("How it connects to the LLM")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: /Learn more about connector tools/ })).toBeInTheDocument();
    });

    test("the learn-more link opens the docs in a new tab", async () => {
        const openSpy = vi.spyOn(window, "open").mockReturnValue(null);
        render(<EventMeshInfoPanel />);
        await userEvent.click(screen.getByRole("button", { name: /Learn more about connector tools/ }));
        expect(openSpy).toHaveBeenCalledWith(DOC_LINKS.eventMeshConnector, "_blank", "noopener,noreferrer");
        openSpy.mockRestore();
    });
});
