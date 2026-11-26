// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { productAnalyticsClient } from "./client";
import { reportAndNotifyError } from "./reportAndNotifyError";

let trackSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
    trackSpy = vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
});

afterEach(() => {
    vi.restoreAllMocks();
});

describe("reportAndNotifyError", () => {
    test("reports the message and shows it as a warning toast", () => {
        const addNotification = vi.fn();

        reportAndNotifyError(addNotification, "Failed to move session", "api");

        expect(trackSpy).toHaveBeenCalledWith("error:displayed", expect.objectContaining({ errorSource: "api" }));
        expect(addNotification).toHaveBeenCalledWith("Failed to move session", "warning");
    });

    test("still reports when no addNotification is available", () => {
        expect(() => reportAndNotifyError(undefined, "Failed to move session", "api")).not.toThrow();
        expect(trackSpy).toHaveBeenCalledWith("error:displayed", expect.objectContaining({ errorSource: "api" }));
    });
});
