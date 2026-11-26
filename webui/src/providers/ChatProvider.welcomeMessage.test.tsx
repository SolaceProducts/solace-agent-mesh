// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { render, screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { UI_MODE, type UIModeName } from "@/providers/contexts/UIModeContext";

import { DEFAULT_WELCOME_MESSAGE, resolveWelcomeMessage } from "@/features/chat/welcomeMessage";

function StatefulWelcomeBubbleProvider({ agentWelcomeMessage, configWelcomeMessage, uiMode, seedWelcomeBubble }: { agentWelcomeMessage: string | undefined; configWelcomeMessage: string | null; uiMode: UIModeName; seedWelcomeBubble: boolean }) {
    const [bubbleText, setBubbleText] = React.useState<string | null>(null);

    React.useEffect(() => {
        if (!seedWelcomeBubble) return;
        setBubbleText(resolveWelcomeMessage(agentWelcomeMessage, configWelcomeMessage, uiMode));
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);

    return <div data-testid="welcome-bubble">{bubbleText ?? ""}</div>;
}

describe("ChatProvider welcome-bubble seeding", () => {
    test("full mode: the agent card's own welcome message wins over the instance config", () => {
        render(<StatefulWelcomeBubbleProvider agentWelcomeMessage="Hi, I'm Acme's support agent." configWelcomeMessage="How can we help?" uiMode={UI_MODE.full} seedWelcomeBubble={true} />);

        expect(screen.getByTestId("welcome-bubble")).toHaveTextContent("Hi, I'm Acme's support agent.");
    });

    test("full mode: the instance config welcome message is used when the agent card has none", () => {
        render(<StatefulWelcomeBubbleProvider agentWelcomeMessage={undefined} configWelcomeMessage="How can we help?" uiMode={UI_MODE.full} seedWelcomeBubble={true} />);

        expect(screen.getByTestId("welcome-bubble")).toHaveTextContent("How can we help?");
    });

    test("full mode: falls back to the hardcoded default when neither the agent card nor the config supplies one", () => {
        render(<StatefulWelcomeBubbleProvider agentWelcomeMessage={undefined} configWelcomeMessage={null} uiMode={UI_MODE.full} seedWelcomeBubble={true} />);

        expect(screen.getByTestId("welcome-bubble")).toHaveTextContent(DEFAULT_WELCOME_MESSAGE);
    });

    test("seedWelcomeBubble=false never seeds the bubble, regardless of mode or config", () => {
        render(<StatefulWelcomeBubbleProvider agentWelcomeMessage={undefined} configWelcomeMessage="How can we help?" uiMode={UI_MODE.full} seedWelcomeBubble={false} />);

        expect(screen.getByTestId("welcome-bubble")).toHaveTextContent("");
    });

    test("agent mode skips the instance config even if seedWelcomeBubble were ever true, falling to the hardcoded default", () => {
        render(<StatefulWelcomeBubbleProvider agentWelcomeMessage={undefined} configWelcomeMessage="How can we help?" uiMode={UI_MODE.agentMode} seedWelcomeBubble={true} />);

        expect(screen.getByTestId("welcome-bubble")).toHaveTextContent(DEFAULT_WELCOME_MESSAGE);
    });

    test("agent mode still honors the agent card's own welcome message", () => {
        render(<StatefulWelcomeBubbleProvider agentWelcomeMessage="Ask me about deployments." configWelcomeMessage="How can we help?" uiMode={UI_MODE.agentMode} seedWelcomeBubble={true} />);

        expect(screen.getByTestId("welcome-bubble")).toHaveTextContent("Ask me about deployments.");
    });
});
