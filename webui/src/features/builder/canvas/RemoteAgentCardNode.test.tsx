// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ComponentProps } from "react";
import { render, screen } from "@testing-library/react";
import { ReactFlowProvider } from "@xyflow/react";
import { describe, expect, test } from "vitest";

import { makeNodeProps } from "@/testing/helpers/nodeProps";

import { RemoteAgentCardNode, remoteAgentPropsEq } from "./RemoteAgentCardNode";

const component = { name: "WeatherProxy", filename: "weather_proxy.json", type: "a2a_agent" };
const canvasData = {
    name: "WeatherProxy",
    filename: "weather_proxy.json",
    type: "a2a_agent",
    a2aAgentMetadata: {
        name: "WeatherProxy",
        agentUrl: "http://ec2-host:9000",
        agentCardLocation: "well_known",
        agentAuthConfig: { type: "bearer", token: "<<__SAM_REQUIRED__>>" },
        description: "Proxies an external weather agent.",
    },
};

describe("remoteAgentPropsEq", () => {
    test("returns true on identical input", () => {
        const a = makeNodeProps({ component, canvasData });
        const b = makeNodeProps({ component, canvasData });
        expect(remoteAgentPropsEq(a, b)).toBe(true);
    });

    test("returns false when component reference changes", () => {
        const a = makeNodeProps({ component, canvasData });
        const b = makeNodeProps({ component: { ...component }, canvasData });
        expect(remoteAgentPropsEq(a, b)).toBe(false);
    });

    test("returns false when selected flips", () => {
        const a = makeNodeProps({ component, canvasData }, false);
        const b = makeNodeProps({ component, canvasData }, true);
        expect(remoteAgentPropsEq(a, b)).toBe(false);
    });
});

describe("RemoteAgentCardNode render", () => {
    function renderCard(data: object) {
        const props = makeNodeProps(data) as ComponentProps<typeof RemoteAgentCardNode>;
        return render(
            <ReactFlowProvider>
                <RemoteAgentCardNode {...props} />
            </ReactFlowProvider>
        );
    }

    test("shows the proxy name, url, card location, and auth type", () => {
        renderCard({ component, canvasData });
        expect(screen.getByText("WeatherProxy")).toBeInTheDocument();
        expect(screen.getByText("External A2A agent")).toBeInTheDocument();
        expect(screen.getByText("http://ec2-host:9000")).toBeInTheDocument();
        expect(screen.getByText("well-known card")).toBeInTheDocument();
        expect(screen.getByText("auth: bearer")).toBeInTheDocument();
    });

    test("falls back to component description and defaults auth to none when metadata is absent", () => {
        renderCard({ component: { ...component, description: "Fallback desc." }, canvasData: { ...canvasData, a2aAgentMetadata: null } });
        expect(screen.getByText("Fallback desc.")).toBeInTheDocument();
        expect(screen.getByText("auth: none")).toBeInTheDocument();
    });

    test("humanizes a slug name for display in a truncating element while hiding the raw slug", () => {
        const slug = { ...component, name: "weather-proxy" };
        renderCard({ component: slug, canvasData: { ...canvasData, name: "weather-proxy" } });
        const title = screen.getByText("Weather Proxy");
        expect(title).toBeInTheDocument();
        expect(title).toHaveClass("truncate");
        expect(screen.queryByText("weather-proxy")).not.toBeInTheDocument();
    });
});
