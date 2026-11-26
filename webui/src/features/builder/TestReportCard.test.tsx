// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { Recommendation, TestReport } from "./TestReportCard";

describe("TestReportCard", () => {
    let TestReportCard: typeof import("./TestReportCard").TestReportCard;

    beforeEach(async () => {
        vi.resetModules();
        vi.doMock("@/lib", () => ({
            cn: (...parts: unknown[]) => parts.filter(Boolean).join(" "),
            Button: ({ children, onClick, disabled, tooltip, ...rest }: { children: React.ReactNode; onClick?: () => void; disabled?: boolean; tooltip?: string }) => (
                <button onClick={onClick} disabled={disabled} title={tooltip} {...rest}>
                    {children}
                </button>
            ),
            Tabs: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
            TabsContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
            TabsList: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
            TabsTrigger: ({ children, onClick }: { children: React.ReactNode; onClick?: () => void }) => <button onClick={onClick}>{children}</button>,
        }));
        const mod = await import("./TestReportCard");
        TestReportCard = mod.TestReportCard;
    });

    const baseReport: TestReport = {
        componentName: "MyAgent",
        componentKind: "agent",
        sampleInput: "hello",
        expectedOutput: "hi",
        actualOutput: "uhh",
        recommendations: [],
    };

    const sampleRec: Recommendation = {
        area: "system_prompt",
        finding: "tone is off",
        suggestion: "tighten the role description",
        priority: "high",
    };

    test("header renders 'Test Report: Test N' for the supplied runNumber", () => {
        render(<TestReportCard report={baseReport} runNumber={3} onApplyChanges={vi.fn()} />);
        screen.getByText(/Test 3/);
    });

    test("falls back to 'Test Report: Test 1' when runNumber is null", () => {
        render(<TestReportCard report={baseReport} runNumber={null} onApplyChanges={vi.fn()} />);
        screen.getByText(/Test 1/);
    });

    test("Apply Changes hidden when there are no recommendations", () => {
        render(<TestReportCard report={baseReport} runNumber={1} onApplyChanges={vi.fn()} />);
        expect(screen.queryByText("Apply Changes")).toBeNull();
        expect(screen.queryByText("Recommendations")).toBeNull();
        expect(screen.getByText("The test was successful. No changes are needed.")).toBeTruthy();
    });

    test("Apply Changes visible when there is at least one recommendation", () => {
        const onApply = vi.fn();
        render(<TestReportCard report={{ ...baseReport, recommendations: [sampleRec] }} runNumber={1} onApplyChanges={onApply} />);
        const applyBtn = screen.getByText("Apply Changes");
        fireEvent.click(applyBtn);
        expect(onApply).toHaveBeenCalledTimes(1);
        expect(onApply.mock.calls[0][0].recommendations[0].finding).toBe("tone is off");
    });

    test("renders the AI summary under the header when there are recommendations", () => {
        const summary = "The agent replied but omitted the citation.";
        render(<TestReportCard report={{ ...baseReport, summary, recommendations: [sampleRec] }} runNumber={1} onApplyChanges={vi.fn()} />);
        expect(screen.getByText(summary)).toBeTruthy();
    });

    test("omits the summary line when no summary is present", () => {
        render(<TestReportCard report={{ ...baseReport, recommendations: [sampleRec] }} runNumber={1} onApplyChanges={vi.fn()} />);
        expect(screen.getByText("Recommendations")).toBeTruthy();
    });

    test("does not show the AI summary in the no-recommendations state", () => {
        const summary = "This should not appear — the static success line wins.";
        render(<TestReportCard report={{ ...baseReport, summary, recommendations: [] }} runNumber={1} onApplyChanges={vi.fn()} />);
        expect(screen.queryByText(summary)).toBeNull();
        expect(screen.getByText("The test was successful. No changes are needed.")).toBeTruthy();
    });

    test("View Test Activity disables itself when the canvas already shows this run", () => {
        render(<TestReportCard report={baseReport} runNumber={1} onApplyChanges={vi.fn()} onViewActivity={vi.fn()} isActivityShown />);
        const viewBtn = screen.getByText("View Test Activity").closest("button") as HTMLButtonElement;
        expect(viewBtn.disabled).toBe(true);
    });

    test("Run Another Test fires its callback", () => {
        const onContinue = vi.fn();
        render(<TestReportCard report={baseReport} runNumber={1} onApplyChanges={vi.fn()} onContinueTesting={onContinue} />);
        fireEvent.click(screen.getByText("Run Another Test"));
        expect(onContinue).toHaveBeenCalledTimes(1);
    });

    test("markdown string output renders formatted text, not a code block", () => {
        const { container } = render(<TestReportCard report={{ ...baseReport, actualOutput: "**bold** and a\n- bullet item" }} runNumber={1} onApplyChanges={vi.fn()} />);
        expect(container.querySelector("strong")).not.toBeNull();
        expect(container.querySelector("li")).not.toBeNull();
        expect(screen.queryByText("**bold** and a")).toBeNull();
    });

    test("structured object output renders inside a JSON code block", () => {
        const { container } = render(<TestReportCard report={{ ...baseReport, expectedOutput: { format: "x", toolsCalled: [] } }} runNumber={1} onApplyChanges={vi.fn()} />);
        const pre = container.querySelector("pre");
        expect(pre).not.toBeNull();
        expect(pre?.textContent).toContain('"format": "x"');
        expect(pre?.textContent).toContain('"toolsCalled"');
    });

    test("JSON-string output is pretty-printed in a code block", () => {
        const { container } = render(<TestReportCard report={{ ...baseReport, actualOutput: '{"a":1}' }} runNumber={1} onApplyChanges={vi.fn()} />);
        const jsonPre = Array.from(container.querySelectorAll("pre")).find(p => p.textContent?.includes('"a": 1'));
        expect(jsonPre).toBeDefined();
    });

    test("a brace-leading string that is not valid JSON falls back to markdown text", () => {
        const { container } = render(<TestReportCard report={{ ...baseReport, actualOutput: "{ not json after all" }} runNumber={1} onApplyChanges={vi.fn()} />);
        expect(container.querySelector("pre")).toBeNull();
        screen.getByText(/not json after all/);
    });

    test("collapse toggle hides and re-shows the report body", () => {
        render(<TestReportCard report={{ ...baseReport, recommendations: [sampleRec] }} runNumber={1} onApplyChanges={vi.fn()} />);
        expect(screen.getByText("tighten the role description")).toBeTruthy();
        fireEvent.click(screen.getByRole("button", { name: "Collapse report" }));
        expect(screen.queryByText("tighten the role description")).toBeNull();
        fireEvent.click(screen.getByRole("button", { name: "Expand report" }));
        expect(screen.getByText("tighten the role description")).toBeTruthy();
    });

    test("recommendation reasoning accordion toggles the finding", () => {
        render(<TestReportCard report={{ ...baseReport, recommendations: [sampleRec] }} runNumber={1} onApplyChanges={vi.fn()} />);
        expect(screen.queryByText("tone is off")).toBeNull();
        fireEvent.click(screen.getByText("Reasoning"));
        expect(screen.getByText("tone is off")).toBeTruthy();
        fireEvent.click(screen.getByRole("button", { name: "Hide reasoning" }));
        expect(screen.queryByText("tone is off")).toBeNull();
    });

    test("empty / nullish output renders the placeholder dash", () => {
        render(<TestReportCard report={{ ...baseReport, sampleInput: "   ", expectedOutput: null, actualOutput: "" }} runNumber={1} onApplyChanges={vi.fn()} />);
        expect(screen.getAllByText("—")).toHaveLength(3);
    });
});
