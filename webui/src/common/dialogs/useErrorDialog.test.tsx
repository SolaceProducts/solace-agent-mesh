// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { productAnalyticsClient } from "@/common/product-analytics/client";

import { useErrorDialog } from "./useErrorDialog";

let trackSpy: ReturnType<typeof vi.spyOn>;
let raise: (error: { title: string; error: string; errorSource?: "api" | "agent_task" | "client" } | null) => void;

beforeEach(() => {
    trackSpy = vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
});

afterEach(() => {
    vi.restoreAllMocks();
});

function Harness() {
    const { ErrorDialog, setError } = useErrorDialog();
    raise = setError;
    return <ErrorDialog />;
}

describe("useErrorDialog reports one error per dialog the user sees", () => {
    test("a second failure while the dialog is already open reports once, not twice", async () => {
        render(<Harness />);

        act(() => raise({ title: "Save failed", error: "boom" }));
        act(() => raise({ title: "Save failed", error: "429 rate limit" }));

        expect(trackSpy).toHaveBeenCalledTimes(1);
        expect(await screen.findByText("429 rate limit")).toBeInTheDocument();
    });

    test("carries the source the raising call site stated", () => {
        render(<Harness />);

        act(() => raise({ title: "Connection Failed", error: "Connection lost. Please try again.", errorSource: "client" }));

        expect(trackSpy).toHaveBeenCalledWith("error:displayed", expect.objectContaining({ errorSource: "client" }));
    });

    test("states no source for a call site that gave none", () => {
        render(<Harness />);

        act(() => raise({ title: "Playback Failed", error: "Please try again." }));

        const [, properties] = trackSpy.mock.calls[0] as [string, Record<string, unknown>];
        expect(properties).not.toHaveProperty("errorSource");
    });

    test("a failure after the dialog is dismissed is a new dialog and reports again", () => {
        render(<Harness />);

        act(() => raise({ title: "Save failed", error: "boom" }));
        act(() => raise(null));
        act(() => raise({ title: "Save failed", error: "429 rate limit" }));

        expect(trackSpy).toHaveBeenCalledTimes(2);
        expect(trackSpy).toHaveBeenLastCalledWith("error:displayed", expect.objectContaining({ errorCategory: "rate_limit" }));
    });
});
