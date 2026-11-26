// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { GatewayTypeChip } from "./GatewayTypeChip";

describe("GatewayTypeChip", () => {
    it("renders the MCP gateway type with its label and icon", () => {
        const { container } = render(<GatewayTypeChip type="mcp" />);
        expect(screen.getByText("MCP")).toBeInTheDocument();
        expect(container.querySelector('svg[viewBox="0 0 27 27"]')).toBeInTheDocument();
    });

    it("renders a known type without a dedicated icon", () => {
        const { container } = render(<GatewayTypeChip type="rest" />);
        expect(screen.getByText("REST API")).toBeInTheDocument();
        expect(container.querySelector("svg")).toBeNull();
    });

    it("labels the agent link type", () => {
        render(<GatewayTypeChip type="agent_link" />);
        expect(screen.getByText("Agent Link")).toBeInTheDocument();
        expect(screen.queryByText("Unknown")).toBeNull();
    });

    it("falls back to Unknown for an unrecognized type", () => {
        render(<GatewayTypeChip type="not-a-real-type" />);
        expect(screen.getByText("Unknown")).toBeInTheDocument();
    });
});
