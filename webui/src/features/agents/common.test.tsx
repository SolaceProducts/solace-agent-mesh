// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { AgentCardInfo } from "@/types";

import { DeploymentStatus } from "./common";

const externalAgent = (overrides: Partial<AgentCardInfo>): AgentCardInfo =>
    ({
        name: "RemoteAgent",
        displayName: "RemoteAgent",
        type: "external",
        ...overrides,
    }) as AgentCardInfo;

describe("DeploymentStatus", () => {
    test("no-card (agentCardLocation 'none') deployed external agent renders 'Registered'", () => {
        renderWithProviders(<DeploymentStatus agent={externalAgent({ agentCardLocation: "none", status: "deployed" })} selectedState="active" />);
        expect(screen.getByText("Registered")).toBeInTheDocument();
    });

    test("card-having deployed + running external agent renders 'Running'", () => {
        renderWithProviders(<DeploymentStatus agent={externalAgent({ agentCardLocation: "well_known", status: "deployed" })} selectedState="active" />);
        expect(screen.getByText("Running")).toBeInTheDocument();
    });

    test("card-having deployed-but-not-running external agent renders 'Not Running'", () => {
        renderWithProviders(<DeploymentStatus agent={externalAgent({ agentCardLocation: "well_known", status: "stopped" })} selectedState="active" />);
        expect(screen.getByText("Not Running")).toBeInTheDocument();
    });

    test("degraded agent renders 'Degraded', not a green 'Running' badge, regardless of selectedState", () => {
        renderWithProviders(<DeploymentStatus agent={externalAgent({ agentCardLocation: "well_known", status: "degraded" })} selectedState="active" />);
        expect(screen.getByText("Degraded")).toBeInTheDocument();
        expect(screen.queryByText("Running")).not.toBeInTheDocument();
    });

    test("no-card agent is never shown as 'Not Running' — liveness is unknowable, so it stays 'Registered'", () => {
        renderWithProviders(<DeploymentStatus agent={externalAgent({ agentCardLocation: "none", status: "deployed" })} selectedState="active" />);
        expect(screen.queryByText("Not Running")).not.toBeInTheDocument();
        expect(screen.getByText("Registered")).toBeInTheDocument();
    });
});
