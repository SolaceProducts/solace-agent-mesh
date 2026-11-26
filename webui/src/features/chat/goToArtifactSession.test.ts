// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, describe, expect, test, vi } from "vitest";

import type { ArtifactWithSession } from "@/api/artifacts";

import { goToArtifactSession } from "./goToArtifactSession";

const ROUTES = { Builder: "/builder" };

function artifact(over: Partial<ArtifactWithSession> = {}): ArtifactWithSession {
    return { filename: "a.txt", sessionId: "s-1", sessionName: null, ...over } as ArtifactWithSession;
}

describe("goToArtifactSession", () => {
    afterEach(() => vi.restoreAllMocks());

    test("agent-owned session dispatches agent-session-navigate and does NOT switch/navigate", async () => {
        const switchSession = vi.fn();
        const navigate = vi.fn();
        const eventSpy = vi.fn();
        window.addEventListener("agent-session-navigate", eventSpy as EventListener);

        await goToArtifactSession(artifact({ sessionId: "s-builder", agentId: "Builder" }), ROUTES, { switchSession, navigate, chatRoute: "/chat" });

        expect(eventSpy).toHaveBeenCalledTimes(1);
        const detail = (eventSpy.mock.calls[0][0] as CustomEvent).detail;
        expect(detail).toEqual({ sessionId: "s-builder", route: "/builder", agentId: "Builder" });
        expect(switchSession).not.toHaveBeenCalled();
        expect(navigate).not.toHaveBeenCalled();

        window.removeEventListener("agent-session-navigate", eventSpy as EventListener);
    });

    test("non-agent artifact switches the session in place and navigates to chat", async () => {
        const switchSession = vi.fn();
        const navigate = vi.fn();
        const eventSpy = vi.fn();
        window.addEventListener("agent-session-navigate", eventSpy as EventListener);

        await goToArtifactSession(artifact({ sessionId: "s-plain", agentId: undefined }), ROUTES, { switchSession, navigate, chatRoute: "/chat" });

        expect(switchSession).toHaveBeenCalledWith("s-plain");
        expect(navigate).toHaveBeenCalledWith("/chat");
        expect(eventSpy).not.toHaveBeenCalled();

        window.removeEventListener("agent-session-navigate", eventSpy as EventListener);
    });

    test("an agent with no route mapping falls back to the chat route", async () => {
        const switchSession = vi.fn();
        const navigate = vi.fn();

        await goToArtifactSession(artifact({ sessionId: "s-x", agentId: "agent_1234" }), ROUTES, { switchSession, navigate, chatRoute: "/chat" });

        expect(switchSession).toHaveBeenCalledWith("s-x");
        expect(navigate).toHaveBeenCalledWith("/chat");
    });

    test("honors a non-default chat route (project-scoped chat)", async () => {
        const switchSession = vi.fn();
        const navigate = vi.fn();

        await goToArtifactSession(artifact({ sessionId: "s-2" }), ROUTES, { switchSession, navigate, chatRoute: "/projects/p1/chat" });

        expect(navigate).toHaveBeenCalledWith("/projects/p1/chat");
    });
});
