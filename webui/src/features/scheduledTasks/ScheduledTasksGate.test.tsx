// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { scheduledTasksGate } from "./ScheduledTasksGate";

const params = { hasNoInvokeAccess: false, hasCardsError: false, onRetry: vi.fn(), isRetrying: false };

describe("scheduledTasksGate", () => {
    test("falls through to the task library when the user can invoke something", () => {
        expect(scheduledTasksGate(params)).toBeNull();
    });

    test("names the missing access when the card list came back empty", () => {
        const { getByText } = render(<>{scheduledTasksGate({ ...params, hasNoInvokeAccess: true })}</>);
        expect(getByText("No Agents or Workflows")).toBeInTheDocument();
    });

    test("reports a failed fetch as an outage rather than a denial", () => {
        const { getByText, queryByText } = render(<>{scheduledTasksGate({ ...params, hasNoInvokeAccess: true, hasCardsError: true })}</>);
        expect(getByText("Unable to Load Agents")).toBeInTheDocument();
        expect(queryByText("No Agents or Workflows")).not.toBeInTheDocument();
    });
});
