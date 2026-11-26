// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { RemoteAgentInterface } from "@/types";

import { ConnectAgentForm } from "./ConnectAgentForm";

const baseDefaults = {
    agentUrl: "https://example.com/agent",
    agentCardUrl: "https://example.com/agent/.well-known/agent-card.json",
};

describe("ConnectAgentForm — Agent Card URL editability", () => {
    test("well-known location renders the card URL read-only (it is derived from the Agent URL)", () => {
        renderWithProviders(<ConnectAgentForm defaultValues={{ ...baseDefaults, agentCardLocation: "well_known" }} />);
        expect(screen.getByTestId("externalAgentInput-agentCardUrl")).toHaveAttribute("readonly");
    });

    test("custom location leaves the card URL editable", () => {
        renderWithProviders(<ConnectAgentForm defaultValues={{ ...baseDefaults, agentCardLocation: "custom" }} />);
        expect(screen.getByTestId("externalAgentInput-agentCardUrl")).not.toHaveAttribute("readonly");
    });
});

describe("ConnectAgentForm — fetched card invalidation", () => {
    test("does not invalidate on a null → card prop transition", () => {
        const onFetchError = vi.fn();
        const defaults = { ...baseDefaults, agentCardLocation: "well_known" as const };
        const card = { name: "test-agent", description: "test" };
        const { rerender } = render(<ConnectAgentForm defaultValues={defaults} fetchedAgentCard={null} onFetchError={onFetchError} />, { wrapper: OpenFeatureTestProvider });
        rerender(<ConnectAgentForm defaultValues={defaults} fetchedAgentCard={card} onFetchError={onFetchError} />);
        expect(onFetchError).not.toHaveBeenCalled();
    });
});

describe("ConnectAgentForm — what the fetched card offers", () => {
    const renderWithCard = (interfaces: RemoteAgentInterface[], signed = false, agentCardLocation: "well_known" | "none" = "well_known") =>
        renderWithProviders(<ConnectAgentForm defaultValues={{ ...baseDefaults, agentCardLocation }} fetchedAgentCard={{ name: "orders-agent", description: "Handles orders.", interfaces, signed }} />);

    test("a card passed in as already fetched shows its details inside the success banner", () => {
        renderWithCard([{ version: "v1_0", binding: "jsonrpc" }], true);
        const banner = screen.getByTestId("messageBanner");
        expect(banner).toHaveTextContent("Successfully fetched agent card: orders-agent");
        expect(within(banner).getByTestId("offeredInterfacesNotice")).toHaveTextContent("Supported interfaces: A2A 1.0 over JSON-RPC");
        expect(within(banner).getByText("Card signature: Signed")).toBeInTheDocument();
    });

    test("closing the banner hides the card details", async () => {
        renderWithCard([{ version: "v1_0", binding: "jsonrpc" }], true);
        await userEvent.click(within(screen.getByTestId("messageBanner")).getByRole("button"));
        expect(screen.queryByTestId("messageBanner")).toBeNull();
        expect(screen.queryByText("Card signature: Signed")).toBeNull();
    });

    test("an agent with no agent card shows no card banner", () => {
        renderWithCard([{ version: "v0_3", binding: "jsonrpc" }], false, "none");
        expect(screen.queryByText(/Successfully fetched agent card/)).toBeNull();
        expect(screen.queryByTestId("offeredInterfacesNotice")).toBeNull();
        expect(screen.queryByText(/offers only A2A 0.3/)).toBeNull();
    });

    test("warns when the card offers only A2A 0.3", () => {
        renderWithCard([{ version: "v0_3", binding: "jsonrpc" }]);
        expect(screen.getByTestId("offeredInterfacesNotice")).toHaveTextContent("Supported interfaces: A2A 0.3 over JSON-RPC");
        expect(screen.getByText("Card signature: Not signed")).toBeInTheDocument();
        expect(within(screen.getByTestId("messageBanner")).queryByText(/offers only A2A 0.3/)).toBeNull();
        expect(screen.getByText("This agent offers only A2A 0.3, an older version of the protocol. It works, but ask its owner for an A2A 1.0 interface.")).toBeInTheDocument();
    });

    test("warns when the card offers no A2A interface the agent mesh can use", () => {
        renderWithCard([]);
        expect(screen.getByText("The agent card offers no A2A interface the agent mesh can use, so the agent will not start. Ask the agent's owner to add an A2A 1.0 or 0.3 interface.")).toBeInTheDocument();
    });

    test("lists an A2A 1.0 interface without a warning", () => {
        renderWithCard([{ version: "v1_0", binding: "http_json" }]);
        expect(screen.getByTestId("offeredInterfacesNotice")).toHaveTextContent("Supported interfaces: A2A 1.0 over HTTP+JSON");
        expect(screen.queryByText(/offers only A2A 0.3/)).toBeNull();
        expect(screen.queryByText(/offers no A2A interface/)).toBeNull();
    });
});
