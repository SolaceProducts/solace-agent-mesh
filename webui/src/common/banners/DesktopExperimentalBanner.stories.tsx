// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useEffect } from "react";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, within } from "storybook/test";

import { paginatedResponse, withDefaults } from "@/testing/helpers";

import { DesktopExperimentalBanner } from "./DesktopExperimentalBanner";

type WailsWindow = { runtime?: { BrowserOpenURL?: (url: string) => void } };

function AsDesktopApp({ children }: { children: ReactNode }) {
    useEffect(() => {
        const w = window as unknown as WailsWindow;
        const previous = w.runtime;
        w.runtime = { ...(previous ?? {}), BrowserOpenURL: () => {} };
        return () => {
            w.runtime = previous;
        };
    }, []);
    return <>{children}</>;
}

const asDesktopApp: Decorator = Story => {
    localStorage.removeItem("desktop-experimental-banner-dismissed");
    return (
        <AsDesktopApp>
            <Story />
        </AsDesktopApp>
    );
};

const capabilitiesFor = (platform: string) =>
    withDefaults(
        http.get("*/api/v1/config/capabilities", () => HttpResponse.json({ data: [{ key: "event_mesh_gateway", name: "Event Mesh Gateway", description: "", status: "stable", platform, hasEnvOverride: false }] })),
        http.get("*/api/v1/config/features", () =>
            HttpResponse.json(paginatedResponse([{ key: "hide_linux_desktop_stage_label", name: "", releasePhase: "experimental", resolved: false, hasEnvOverride: false, registryDefault: false, description: "" }]))
        )
    );

const meta = {
    title: "Common/DesktopExperimentalBanner",
    component: DesktopExperimentalBanner,
    decorators: [
        Story => (
            <div className="max-w-2xl">
                <Story />
            </div>
        ),
    ],
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "Informational, dismissible notice shown only in the desktop app running on Linux, where the bundle is Experimental. Hidden on macOS/Windows desktop and in any browser (including a production Linux gateway).",
            },
        },
    },
} satisfies Meta<typeof DesktopExperimentalBanner>;

export default meta;
type Story = StoryObj<typeof meta>;

export const LinuxDesktop: Story = {
    decorators: [asDesktopApp],
    parameters: { msw: { handlers: capabilitiesFor("linux") } },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByTestId("messageBanner")).toBeInTheDocument();
    },
};
