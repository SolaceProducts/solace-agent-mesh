// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { PageSidePanelWrapper } from "./PageSidePanelWrapper";

const panel = <div data-testid="panelContent">Details</div>;

function overlay(isSidePanelOpen: boolean, onCloseComplete: () => void) {
    return <PageSidePanelWrapper variant="overlay" sidepanel={panel} isSidePanelOpen={isSidePanelOpen} onCloseComplete={onCloseComplete} />;
}

const slider = () => screen.getByTestId("panelContent").parentElement as HTMLElement;

describe("PageSidePanelWrapper overlay", () => {
    beforeEach(() => {
        vi.useFakeTimers();
    });

    afterEach(() => {
        vi.useRealTimers();
    });

    async function openFully(onCloseComplete: () => void) {
        const view = render(overlay(true, onCloseComplete));
        await act(async () => {
            await vi.advanceTimersToNextFrame();
        });
        return view;
    }

    test("unmounts and reports the close once its slide out ends", async () => {
        const onCloseComplete = vi.fn();
        const { rerender } = await openFully(onCloseComplete);
        rerender(overlay(false, onCloseComplete));
        fireEvent.transitionEnd(slider(), { propertyName: "translate" });
        expect(screen.queryByTestId("panelContent")).not.toBeInTheDocument();
        await act(async () => {
            await vi.advanceTimersByTimeAsync(2_000);
        });
        expect(onCloseComplete).toHaveBeenCalledOnce();
    });

    test("still unmounts when the browser never animates the close, as when it closes before its slide in began", async () => {
        const onCloseComplete = vi.fn();
        const { rerender } = await openFully(onCloseComplete);
        rerender(overlay(false, onCloseComplete));
        expect(screen.getByTestId("panelContent")).toBeInTheDocument();
        await act(async () => {
            await vi.advanceTimersByTimeAsync(1_000);
        });
        expect(screen.queryByTestId("panelContent")).not.toBeInTheDocument();
        expect(onCloseComplete).toHaveBeenCalledOnce();
    });

    test("reopening before the close finishes keeps the panel", async () => {
        const onCloseComplete = vi.fn();
        const { rerender } = await openFully(onCloseComplete);
        rerender(overlay(false, onCloseComplete));
        rerender(overlay(true, onCloseComplete));
        await act(async () => {
            await vi.advanceTimersByTimeAsync(2_000);
        });
        expect(screen.getByTestId("panelContent")).toBeInTheDocument();
        expect(onCloseComplete).not.toHaveBeenCalled();
    });
});
