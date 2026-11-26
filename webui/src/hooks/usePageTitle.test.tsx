// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, render } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { beforeEach, describe, expect, test } from "vitest";

import type { ConfigContextValue } from "@/providers/contexts/ConfigContext";
import { ConfigContext } from "@/providers/contexts/ConfigContext";

import { type NavItemConfig } from "@/types/nav";

import { usePageTitle } from "./usePageTitle";

function makeConfigValue(overrides: Partial<ConfigContextValue> = {}): ConfigContextValue {
    return {
        webuiServerUrl: "",
        platformServerUrl: "",
        configAuthLoginUrl: "",
        configUseAuthorization: false,
        configWelcomeMessage: "",
        configDisclaimerText: "",
        configRedirectUrl: "",
        configCollectFeedback: false,
        configAppName: "",
        configLogoUrl: "",
        configSmallLogoUrl: "",
        frontend_use_authorization: false,
        platformConfigured: false,
        identityServiceType: null,
        claimKey: "",
        refetch: async () => {},
        ...overrides,
    };
}

const NAV_ITEMS: NavItemConfig[] = [
    { id: "agentMesh", label: "Agent Mesh", route: "/agent-mesh", routeMatch: "/agent-mesh" },
    { id: "hiddenSchedules", label: "Schedules", route: "/scheduled-tasks", routeMatch: "/scheduled-tasks", hidden: true },
    {
        id: "assets",
        label: "Assets",
        children: [{ id: "prompts", label: "Prompts", route: "/prompts", routeMatch: "/prompts" }],
    },
];

function HookHost({ items }: { items: NavItemConfig[] }) {
    usePageTitle(items);
    return null;
}

function renderAtRoute(initialPath: string, appName: string, items: NavItemConfig[] = NAV_ITEMS) {
    const router = createMemoryRouter([{ path: "*", element: <HookHost items={items} /> }], { initialEntries: [initialPath] });
    render(
        <ConfigContext.Provider value={makeConfigValue({ configAppName: appName })}>
            <RouterProvider router={router} />
        </ConfigContext.Provider>
    );
    return router;
}

describe("usePageTitle", () => {
    beforeEach(() => {
        document.title = "Solace Agent Mesh";
    });

    test.each([
        ["/agent-mesh", "Acme | Agent Mesh"],
        ["/prompts", "Acme | Prompts"],
        ["/chat", "Acme"],
        ["/recent-chats", "Acme"],
        ["/agent-mesh/0b7a3c1e-2f34-4d56-8a9b-0c1d2e3f4a5b", "Acme"],
        ["/scheduled-tasks", "Acme"],
    ])("route %s titles the tab %s", (route, expected) => {
        renderAtRoute(route, "Acme");
        expect(document.title).toBe(expected);
    });

    test("navigating between matched and unmatched routes updates the title both ways", async () => {
        const router = renderAtRoute("/agent-mesh", "Acme");
        expect(document.title).toBe("Acme | Agent Mesh");

        await act(() => router.navigate("/chat"));
        expect(document.title).toBe("Acme");

        await act(() => router.navigate("/agent-mesh"));
        expect(document.title).toBe("Acme | Agent Mesh");
    });

    test("falls back to the page label when the app name is cleared", () => {
        renderAtRoute("/agent-mesh", "");
        expect(document.title).toBe("Agent Mesh");
    });

    test("falls back to the product name when there is neither an app name nor a label", () => {
        document.title = "stale";
        renderAtRoute("/unmatched-route", "");
        expect(document.title).toBe("Solace Agent Mesh");
    });

    test("a cleared app name does not leave the previous page's label in the tab", () => {
        const router = createMemoryRouter([{ path: "*", element: <HookHost items={NAV_ITEMS} /> }], { initialEntries: ["/agent-mesh"] });
        render(
            <ConfigContext.Provider value={makeConfigValue({ configAppName: "" })}>
                <RouterProvider router={router} />
            </ConfigContext.Provider>
        );
        expect(document.title).toBe("Agent Mesh");

        act(() => {
            void router.navigate("/chat");
        });

        expect(document.title).not.toBe("Agent Mesh");
    });

    test("retitles when the app name changes without navigating", () => {
        const router = createMemoryRouter([{ path: "*", element: <HookHost items={NAV_ITEMS} /> }], { initialEntries: ["/agent-mesh"] });
        const { rerender } = render(
            <ConfigContext.Provider value={makeConfigValue({ configAppName: "Acme" })}>
                <RouterProvider router={router} />
            </ConfigContext.Provider>
        );
        expect(document.title).toBe("Acme | Agent Mesh");

        rerender(
            <ConfigContext.Provider value={makeConfigValue({ configAppName: "Globex" })}>
                <RouterProvider router={router} />
            </ConfigContext.Provider>
        );
        expect(document.title).toBe("Globex | Agent Mesh");
    });
});
