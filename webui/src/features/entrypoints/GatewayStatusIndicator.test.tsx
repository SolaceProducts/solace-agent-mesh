// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import type { Gateway } from "@/types";

import { GatewayStatusIndicator } from "./GatewayStatusIndicator";

const gateway = (overrides: Partial<Gateway>): Gateway =>
    ({
        name: "MyGateway",
        description: "",
        type: "http_sse",
        values: {},
        ...overrides,
    }) as Gateway;

describe("GatewayStatusIndicator", () => {
    test("deployed + running renders 'Running'", () => {
        render(<GatewayStatusIndicator gateway={gateway({ deploymentStatus: "deployed", runtimeStatus: { status: "running" } })} />);
        expect(screen.getByText("Running")).toBeInTheDocument();
    });

    test("deployed + degraded renders 'Degraded', not 'Running'", () => {
        render(
            <GatewayStatusIndicator
                gateway={gateway({
                    deploymentStatus: "deployed",
                    runtimeStatus: { status: "degraded", problems: [{ id: "queue:x", severity: "degraded", detail: "boom", since: "" }] },
                })}
            />
        );
        expect(screen.getByText("Degraded")).toBeInTheDocument();
        expect(screen.queryByText("Running")).not.toBeInTheDocument();
    });

    test("deployed + disconnected renders 'Inactive'", () => {
        render(<GatewayStatusIndicator gateway={gateway({ deploymentStatus: "deployed", runtimeStatus: { status: "disconnected" } })} />);
        expect(screen.getByText("Inactive")).toBeInTheDocument();
    });

    test("not deployed shows no runtime badge", () => {
        render(<GatewayStatusIndicator gateway={gateway({ deploymentStatus: "not_deployed" })} />);
        expect(screen.queryByText("Running")).not.toBeInTheDocument();
        expect(screen.queryByText("Degraded")).not.toBeInTheDocument();
        expect(screen.queryByText("Inactive")).not.toBeInTheDocument();
    });

    test("not deployed shows no health problems indicator even if runtimeStatus carries stale problems", () => {
        render(
            <GatewayStatusIndicator
                gateway={gateway({
                    deploymentStatus: "not_deployed",
                    runtimeStatus: { status: "degraded", problems: [{ id: "queue:x", severity: "degraded", detail: "boom", since: "" }] },
                })}
            />
        );
        expect(screen.queryByTestId("gatewayHealth-indicator")).not.toBeInTheDocument();
    });

    test("showConnectionStatus=false still shows the health problems indicator", () => {
        render(
            <GatewayStatusIndicator
                showConnectionStatus={false}
                gateway={gateway({
                    deploymentStatus: "deployed",
                    runtimeStatus: { status: "degraded", problems: [{ id: "queue:x", severity: "degraded", detail: "boom", since: "" }] },
                })}
            />
        );
        expect(screen.queryByTestId("gatewayHealth-indicator")).toBeInTheDocument();
    });

    test("deployed + degraded shows the health problems indicator", () => {
        render(
            <GatewayStatusIndicator
                gateway={gateway({
                    deploymentStatus: "deployed",
                    runtimeStatus: { status: "degraded", problems: [{ id: "queue:x", severity: "degraded", detail: "boom", since: "" }] },
                })}
            />
        );
        expect(screen.getByTestId("gatewayHealth-indicator")).toBeInTheDocument();
    });
});
