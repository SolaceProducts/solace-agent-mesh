// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, screen } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import * as capabilitiesApi from "@/api/capabilities";
import * as featuresApi from "@/api/features";

import * as useIsDesktopHook from "@/hooks/useIsDesktop";

import { EXTERNAL_DOC_LINKS } from "@/utils/docs";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { DesktopExperimentalBanner } from "./DesktopExperimentalBanner";

function mockCapabilities(platform: string | undefined) {
    const data = platform ? [{ key: "event_mesh_gateway", name: "", description: "", status: "stable", platform, hasEnvOverride: false }] : undefined;
    vi.spyOn(capabilitiesApi, "useCapabilities").mockReturnValue({ data } as ReturnType<typeof capabilitiesApi.useCapabilities>);
}

function mockHideFlag(resolved: boolean | null) {
    const data = resolved === null ? undefined : [{ key: "hide_linux_desktop_stage_label", name: "", releasePhase: "experimental", resolved, hasEnvOverride: false, registryDefault: false, description: "" }];
    vi.spyOn(featuresApi, "useFeatureFlags").mockReturnValue({ data } as ReturnType<typeof featuresApi.useFeatureFlags>);
}

function onLinuxDesktop() {
    vi.spyOn(useIsDesktopHook, "useIsDesktop").mockReturnValue(true);
    mockCapabilities("linux");
    mockHideFlag(false);
}

describe("DesktopExperimentalBanner", () => {
    beforeEach(() => {
        vi.restoreAllMocks();
        localStorage.clear();
    });

    test("renders on the Linux desktop app, linking Experimental to the release-stages doc", () => {
        onLinuxDesktop();
        renderWithProviders(<DesktopExperimentalBanner />);
        expect(screen.getByTestId("messageBanner")).toBeInTheDocument();
        expect(screen.getByRole("link", { name: "Experimental" })).toHaveAttribute("href", EXTERNAL_DOC_LINKS.releaseStages);
    });

    test("renders nothing when the hide flag is on", () => {
        onLinuxDesktop();
        mockHideFlag(true);
        renderWithProviders(<DesktopExperimentalBanner />);
        expect(screen.queryByTestId("messageBanner")).not.toBeInTheDocument();
    });

    test("renders nothing before feature flags load (no flash when hidden)", () => {
        onLinuxDesktop();
        mockHideFlag(null);
        renderWithProviders(<DesktopExperimentalBanner />);
        expect(screen.queryByTestId("messageBanner")).not.toBeInTheDocument();
    });

    test.each(["darwin", "windows"])("renders nothing on the %s desktop app", platform => {
        vi.spyOn(useIsDesktopHook, "useIsDesktop").mockReturnValue(true);
        mockCapabilities(platform);
        renderWithProviders(<DesktopExperimentalBanner />);
        expect(screen.queryByTestId("messageBanner")).not.toBeInTheDocument();
    });

    test("renders nothing in a browser served by a Linux gateway (not the desktop app)", () => {
        vi.spyOn(useIsDesktopHook, "useIsDesktop").mockReturnValue(false);
        mockCapabilities("linux");
        renderWithProviders(<DesktopExperimentalBanner />);
        expect(screen.queryByTestId("messageBanner")).not.toBeInTheDocument();
    });

    test("renders nothing before capabilities load (no flash)", () => {
        vi.spyOn(useIsDesktopHook, "useIsDesktop").mockReturnValue(true);
        mockCapabilities(undefined);
        renderWithProviders(<DesktopExperimentalBanner />);
        expect(screen.queryByTestId("messageBanner")).not.toBeInTheDocument();
    });

    test("can be dismissed, and stays dismissed across remounts", () => {
        onLinuxDesktop();
        const { unmount } = renderWithProviders(<DesktopExperimentalBanner />);
        expect(screen.getByTestId("messageBanner")).toBeInTheDocument();

        fireEvent.click(screen.getByRole("button", { name: /dismiss/i }));
        expect(screen.queryByTestId("messageBanner")).not.toBeInTheDocument();

        unmount();
        renderWithProviders(<DesktopExperimentalBanner />);
        expect(screen.queryByTestId("messageBanner")).not.toBeInTheDocument();
    });
});
