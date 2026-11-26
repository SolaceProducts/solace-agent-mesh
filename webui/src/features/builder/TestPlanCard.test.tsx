// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { TestPlan } from "./TestPlanCard";

describe("TestPlanCard", () => {
    let TestPlanCard: typeof import("./TestPlanCard").TestPlanCard;

    beforeEach(async () => {
        vi.resetModules();
        vi.doMock("@/ui/button", () => ({
            Button: ({ children, onClick, disabled, tooltip }: { children: React.ReactNode; onClick?: () => void; disabled?: boolean; tooltip?: string }) => (
                <button onClick={onClick} disabled={disabled} title={tooltip}>
                    {children}
                </button>
            ),
        }));
        vi.doMock("@/ui/textarea", () => ({
            Textarea: (props: React.ComponentProps<"textarea">) => <textarea {...props} />,
        }));
        vi.doMock("@/ui/tabs", () => ({
            Tabs: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
            TabsContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
            TabsList: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
            TabsTrigger: ({ children, onClick }: { children: React.ReactNode; onClick?: () => void }) => <button onClick={onClick}>{children}</button>,
        }));
        vi.doMock("@/ui/dialog", () => ({
            Dialog: ({ children, open }: { children: React.ReactNode; open?: boolean }) => (open ? <div>{children}</div> : null),
            DialogContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
            DialogHeader: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
            DialogTitle: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
            DialogDescription: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
            DialogFooter: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
        }));

        const mod = await import("./TestPlanCard");
        TestPlanCard = mod.TestPlanCard;
    });

    const samplePlan: TestPlan = {
        componentName: "MyAgent",
        componentKind: "agent",
        sampleInput: "hello",
        expectedOutput: { ok: true },
        rationale: "happy path",
    };

    test("renders component name in header", () => {
        render(<TestPlanCard plan={samplePlan} onStartTest={vi.fn()} onCancel={vi.fn()} />);
        screen.getByText(/MyAgent/);
    });

    test("Start Test fires with the original plan when not edited", () => {
        const onStartTest = vi.fn();
        render(<TestPlanCard plan={samplePlan} onStartTest={onStartTest} onCancel={vi.fn()} />);
        fireEvent.click(screen.getByText("Start Test"));
        expect(onStartTest).toHaveBeenCalledTimes(1);
        const got = onStartTest.mock.calls[0][0] as TestPlan;
        expect(got.componentName).toBe("MyAgent");
        expect(got.sampleInput).toBe("hello");
    });

    test("Cancel fires onCancel", () => {
        const onCancel = vi.fn();
        render(<TestPlanCard plan={samplePlan} onStartTest={vi.fn()} onCancel={onCancel} />);
        fireEvent.click(screen.getByText("Cancel"));
        expect(onCancel).toHaveBeenCalledTimes(1);
    });

    test("busy=true disables Start Test and Cancel", () => {
        render(<TestPlanCard plan={samplePlan} onStartTest={vi.fn()} onCancel={vi.fn()} busy />);
        const start = screen.getByText("Start Test").closest("button") as HTMLButtonElement;
        const cancel = screen.getByText("Cancel").closest("button") as HTMLButtonElement;
        expect(start.disabled).toBe(true);
        expect(cancel.disabled).toBe(true);
    });

    test("Edit opens the dialog seeded with the current field values", () => {
        render(<TestPlanCard plan={samplePlan} onStartTest={vi.fn()} onCancel={vi.fn()} />);
        expect(screen.queryAllByRole("textbox")).toHaveLength(0);
        fireEvent.click(screen.getByTitle("Edit"));
        const fields = screen.getAllByRole("textbox") as HTMLTextAreaElement[];
        expect(fields).toHaveLength(2);
        expect(fields[0].value).toBe("hello");
        expect(fields[1].value).toBe(JSON.stringify({ ok: true }, null, 2));
    });

    test("editing in the dialog and saving updates the plan sent to Start Test", () => {
        const onStartTest = vi.fn();
        render(<TestPlanCard plan={samplePlan} onStartTest={onStartTest} onCancel={vi.fn()} />);
        fireEvent.click(screen.getByTitle("Edit"));
        const fields = screen.getAllByRole("textbox") as HTMLTextAreaElement[];
        fireEvent.change(fields[0], { target: { value: "goodbye" } });
        fireEvent.click(screen.getByText("Save"));
        expect(screen.queryAllByRole("textbox")).toHaveLength(0);
        fireEvent.click(screen.getByText("Start Test"));
        const got = onStartTest.mock.calls[0][0] as TestPlan;
        expect(got.sampleInput).toBe("goodbye");
        expect(got.expectedOutput).toEqual({ ok: true });
    });

    test("Discard Changes closes the dialog without altering the plan", () => {
        const onStartTest = vi.fn();
        render(<TestPlanCard plan={samplePlan} onStartTest={onStartTest} onCancel={vi.fn()} />);
        fireEvent.click(screen.getByTitle("Edit"));
        const fields = screen.getAllByRole("textbox") as HTMLTextAreaElement[];
        fireEvent.change(fields[0], { target: { value: "goodbye" } });
        fireEvent.click(screen.getByText("Discard Changes"));
        fireEvent.click(screen.getByText("Start Test"));
        const got = onStartTest.mock.calls[0][0] as TestPlan;
        expect(got.sampleInput).toBe("hello");
    });

    test("invalid JSON in a field disables Save", () => {
        render(<TestPlanCard plan={samplePlan} onStartTest={vi.fn()} onCancel={vi.fn()} />);
        fireEvent.click(screen.getByTitle("Edit"));
        const fields = screen.getAllByRole("textbox") as HTMLTextAreaElement[];
        fireEvent.change(fields[0], { target: { value: "{ not valid" } });
        const save = screen.getByText("Save").closest("button") as HTMLButtonElement;
        expect(save.disabled).toBe(true);
    });
});
