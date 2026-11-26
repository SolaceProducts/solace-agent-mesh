// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { ApplyChangesCardData } from "./ApplyChangesCard";

describe("ApplyChangesCard", () => {
    let ApplyChangesCard: typeof import("./ApplyChangesCard").ApplyChangesCard;

    beforeEach(async () => {
        vi.resetModules();
        vi.doMock("@/ui/button", () => ({
            Button: ({ children, onClick }: { children: React.ReactNode; onClick?: () => void }) => <button onClick={onClick}>{children}</button>,
        }));
        const mod = await import("./ApplyChangesCard");
        ApplyChangesCard = mod.ApplyChangesCard;
    });

    const data: ApplyChangesCardData = {
        componentName: "TextSummarizer",
        componentKind: "agent",
        items: [
            { priority: "medium", area: "tools", finding: "missing validation" },
            { priority: "high", area: "system_prompt", finding: "not enforcing JSON output" },
            { priority: "high", area: "system_prompt", finding: "weak output-format contract" },
        ],
    };

    test("renders the minimal header with component kind + name", () => {
        render(<ApplyChangesCard data={data} onViewTestReport={vi.fn()} />);
        screen.getByText(/Apply these changes to the agent/);
        screen.getByText("TextSummarizer");
    });

    test("shows each finding as a one-line title with its area label", () => {
        render(<ApplyChangesCard data={data} onViewTestReport={vi.fn()} />);
        screen.getByText("not enforcing JSON output");
        screen.getByText("weak output-format contract");
        screen.getByText("missing validation");
        expect(screen.getAllByText("System Prompt").length).toBe(2);
        screen.getByText("Tools");
    });

    test("groups by priority with counts, high before medium", () => {
        render(<ApplyChangesCard data={data} onViewTestReport={vi.fn()} />);
        const high = screen.getByText("High priority (2)");
        const medium = screen.getByText("Medium priority (1)");
        expect(high.compareDocumentPosition(medium) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    });

    test("does NOT leak agent-directed plumbing (mark_changes_applied, test I/O)", () => {
        render(<ApplyChangesCard data={data} onViewTestReport={vi.fn()} />);
        expect(screen.queryByText(/mark_changes_applied/)).toBeNull();
        expect(screen.queryByText(/Sample input/i)).toBeNull();
    });

    test("View test report fires the callback", () => {
        const onViewTestReport = vi.fn();
        render(<ApplyChangesCard data={data} onViewTestReport={onViewTestReport} />);
        fireEvent.click(screen.getByText("View test report"));
        expect(onViewTestReport).toHaveBeenCalledTimes(1);
    });

    test("empty recommendations renders a graceful message, no priority sections", () => {
        render(<ApplyChangesCard data={{ ...data, items: [] }} onViewTestReport={vi.fn()} />);
        screen.getByText("No changes were recommended.");
        expect(screen.queryByText(/High priority/)).toBeNull();
    });
});
