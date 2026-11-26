// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { useUIModeContext } from "@/providers/contexts";
import { UIModeProvider } from "@/providers/UIModeProvider";

function Probe() {
    const ui = useUIModeContext();
    return (
        <div
            data-testid="probe"
            data-mode={ui.mode}
            data-shownavigationsidebar={String(ui.showNavigationSidebar)}
            data-showagentselector={String(ui.showAgentSelector)}
            data-showactivity={String(ui.showActivityPanel)}
            data-allowprompts={String(ui.allowPrompts)}
            data-showrecentchats={String(ui.showRecentChatsPanel)}
            data-pinnedagentname={ui.pinnedAgentName ?? ""}
        />
    );
}

function renderAt(url: string) {
    window.history.replaceState({}, "", url);
    render(
        <UIModeProvider>
            <Probe />
        </UIModeProvider>
    );
    return screen.getByTestId("probe");
}

describe("computeUIMode (via UIModeProvider)", () => {
    afterEach(() => {
        window.history.replaceState({}, "", "/");
    });

    it("defaults to the full UI", () => {
        const el = renderAt("/#/chat");
        expect(el.dataset.mode).toBe("full");
        expect(el.dataset.shownavigationsidebar).toBe("true");
        expect(el.dataset.showagentselector).toBe("true");
    });

    it("agentMode on /agent-mode/*, capturing the pinned agent", () => {
        const el = renderAt("/#/agent-mode/chat?agent=Foo");
        expect(el.dataset.mode).toBe("agentMode");
        expect(el.dataset.pinnedagentname).toBe("Foo");
        expect(el.dataset.shownavigationsidebar).toBe("false");
        expect(el.dataset.showagentselector).toBe("false");
        expect(el.dataset.showactivity).toBe("false");
        expect(el.dataset.allowprompts).toBe("false");
        expect(el.dataset.showrecentchats).toBe("true");
    });
});
