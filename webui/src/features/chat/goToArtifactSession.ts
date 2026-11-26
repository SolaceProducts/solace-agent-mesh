// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ArtifactWithSession } from "@/api/artifacts";

import { resolveAgentSessionRoute } from "@/utils/agentUtils";

interface GoToArtifactSessionDeps {
    switchSession: (sessionId: string) => Promise<void | boolean> | void;
    navigate: (to: string) => void;
    chatRoute: string;
}

export async function goToArtifactSession(artifact: Pick<ArtifactWithSession, "sessionId" | "agentId">, agentSessionRoutes: Record<string, string> | undefined, { switchSession, navigate, chatRoute }: GoToArtifactSessionDeps): Promise<void> {
    const route = resolveAgentSessionRoute(artifact.agentId, agentSessionRoutes) || chatRoute;
    if (route !== chatRoute) {
        window.dispatchEvent(
            new CustomEvent("agent-session-navigate", {
                detail: { sessionId: artifact.sessionId, route, agentId: artifact.agentId },
            })
        );
        return;
    }
    await switchSession(artifact.sessionId);
    navigate(chatRoute);
}
