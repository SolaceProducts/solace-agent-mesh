// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { TaskConfig } from "./task-config";
import { TaskPreviewPanel } from "./TaskPreviewPanel";

vi.mock("./useTargetDisplayName", () => ({
    useTargetDisplayName: (name: string) => name,
}));

const baseConfig: TaskConfig = {
    name: "Test Task",
    description: "Test Description",
    scheduleType: "cron",
    scheduleExpression: "0 0 * * *",
    timezone: "UTC",
    targetType: "agent",
    targetAgentName: "test-agent",
    taskMessage: "Test message",
    enabled: true,
};

const emptyConfig: TaskConfig = {
    name: "",
    description: "",
    scheduleType: "cron",
    scheduleExpression: "",
    timezone: "",
    targetType: "agent",
    targetAgentName: "",
    taskMessage: "",
    enabled: true,
};

describe("TaskPreviewPanel", () => {
    describe("empty state", () => {
        it("shows 'No Scheduled Task Yet' when name is empty", () => {
            render(<TaskPreviewPanel config={emptyConfig} highlightedFields={[]} />);
            expect(screen.getByText("No Scheduled Task Yet")).toBeInTheDocument();
        });

        it("shows 'No Scheduled Task Yet' when name is whitespace only", () => {
            render(<TaskPreviewPanel config={{ ...emptyConfig, name: "   " }} highlightedFields={[]} />);
            expect(screen.getByText("No Scheduled Task Yet")).toBeInTheDocument();
        });
    });

    describe("content state", () => {
        it("shows the task name when set", () => {
            render(<TaskPreviewPanel config={baseConfig} highlightedFields={[]} />);
            expect(screen.getByText("Test Task")).toBeInTheDocument();
        });

        it("does not show 'No Scheduled Task Yet' when name is set", () => {
            render(<TaskPreviewPanel config={baseConfig} highlightedFields={[]} />);
            expect(screen.queryByText("No Scheduled Task Yet")).not.toBeInTheDocument();
        });
    });
});
