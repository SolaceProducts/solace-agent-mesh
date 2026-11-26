// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

const handleNewSession = vi.fn();
const setSelectedAgentName = vi.fn();
const navigateSpy = vi.fn();

const scenario = { pathname: "/agent-mesh", builderMode: false };

describe("BuilderModeEffects — session reset on builder entry", () => {
    let BuilderModeEffects: React.ComponentType;

    beforeEach(async () => {
        vi.resetModules();
        handleNewSession.mockReset();
        setSelectedAgentName.mockReset();
        navigateSpy.mockReset();
        sessionStorage.clear();
        scenario.pathname = "/agent-mesh";
        scenario.builderMode = false;

        vi.doMock("@/providers/contexts", () => ({
            useBuilderContext: () => ({ builderMode: scenario.builderMode }),
            useChatContext: () => ({ selectedAgentName: "WeatherAgent", setSelectedAgentName, handleNewSession }),
        }));
        vi.doMock("@/providers/contexts/BuilderContext", () => ({ BUILDER_AGENT_NAME: "Builder" }));
        vi.doMock("react-router-dom", async importOriginal => ({
            ...(await importOriginal<typeof import("react-router-dom")>()),
            useLocation: () => ({ pathname: scenario.pathname }),
            useNavigate: () => navigateSpy,
        }));
        vi.doMock("@/api/client", () => ({ api: { webui: { get: vi.fn() } } }));
        vi.doMock("@/utils/agentUtils", () => ({ isEphemeralTestAgentName: () => false }));

        ({ BuilderModeEffects } = await import("./BuilderEffects"));
    });

    const enterBuilderFrom = (initialPathname: string) => {
        scenario.pathname = initialPathname;
        const { rerender } = render(<BuilderModeEffects />);
        scenario.pathname = "/builder";
        scenario.builderMode = true;
        rerender(<BuilderModeEffects />);
        return rerender;
    };

    const leaveBuilder = (rerender: (ui: React.ReactElement) => void) => {
        scenario.pathname = "/chat";
        scenario.builderMode = false;
        rerender(<BuilderModeEffects />);
    };

    const reenterBuilder = (rerender: (ui: React.ReactElement) => void) => {
        scenario.pathname = "/builder";
        scenario.builderMode = true;
        rerender(<BuilderModeEffects />);
    };

    test("navigating from chat to Quick Build starts a fresh session", () => {
        enterBuilderFrom("/agent-mesh");
        expect(handleNewSession).toHaveBeenCalledTimes(1);
    });

    test("a genuine refresh landing on /builder preserves the existing session", () => {
        enterBuilderFrom("/builder");
        expect(handleNewSession).not.toHaveBeenCalled();
    });

    test("after a refresh into /builder, leaving and returning starts a fresh session", () => {
        const rerender = enterBuilderFrom("/builder");
        expect(handleNewSession).not.toHaveBeenCalled();

        leaveBuilder(rerender);
        reenterBuilder(rerender);
        expect(handleNewSession).toHaveBeenCalledTimes(1);
    });

    test("repeated Quick Build visits from chat each start a fresh session", () => {
        const rerender = enterBuilderFrom("/agent-mesh");
        expect(handleNewSession).toHaveBeenCalledTimes(1);

        leaveBuilder(rerender);
        reenterBuilder(rerender);
        expect(handleNewSession).toHaveBeenCalledTimes(2);
    });

    test("a pending sidebar builder-session redirect is preserved (no reset)", () => {
        scenario.pathname = "/agent-mesh";
        const { rerender } = render(<BuilderModeEffects />);

        sessionStorage.setItem("sam_agent_session_redirect", "sess-1");
        scenario.pathname = "/builder";
        scenario.builderMode = true;
        rerender(<BuilderModeEffects />);

        expect(handleNewSession).not.toHaveBeenCalled();
    });

    test("a pending test-session pin is preserved (no reset)", () => {
        scenario.pathname = "/agent-mesh";
        const { rerender } = render(<BuilderModeEffects />);

        sessionStorage.setItem("sam_test_session_pin", "test-sess-1");
        scenario.pathname = "/builder";
        scenario.builderMode = true;
        rerender(<BuilderModeEffects />);

        expect(handleNewSession).not.toHaveBeenCalled();
    });

    test("entering builder selects the Builder agent and navigates to /builder", () => {
        enterBuilderFrom("/agent-mesh");
        expect(setSelectedAgentName).toHaveBeenCalledWith("Builder");
        expect(navigateSpy).toHaveBeenCalledWith("/builder");
    });

    test("builder mode activating before the route commits still starts a fresh session", () => {
        scenario.pathname = "/agent-mesh";
        const { rerender } = render(<BuilderModeEffects />);

        scenario.builderMode = true;
        rerender(<BuilderModeEffects />);

        expect(handleNewSession).toHaveBeenCalledTimes(1);
    });

    test("toggling builder off while still on /builder restores the previous agent and returns to /chat", () => {
        const rerender = enterBuilderFrom("/agent-mesh");
        setSelectedAgentName.mockClear();
        navigateSpy.mockClear();

        scenario.builderMode = false;
        rerender(<BuilderModeEffects />);

        expect(setSelectedAgentName).toHaveBeenCalledWith("WeatherAgent");
        expect(navigateSpy).toHaveBeenCalledWith("/chat");
    });

    test("toggling builder off after navigating away does not force a redirect to /chat", () => {
        const rerender = enterBuilderFrom("/agent-mesh");
        setSelectedAgentName.mockClear();
        navigateSpy.mockClear();

        scenario.pathname = "/workflows";
        scenario.builderMode = false;
        rerender(<BuilderModeEffects />);

        expect(setSelectedAgentName).toHaveBeenCalledWith("WeatherAgent");
        expect(navigateSpy).not.toHaveBeenCalled();
    });
});
