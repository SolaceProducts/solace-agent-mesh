// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { DroppedResource, RemovalResult } from "@/api/builder/applyTypes";

import { captureRadixDialogDiagnostics } from "@/testing/helpers/radixDialogDiagnostics";

import { RemoveDroppedDialog } from "./RemoveDroppedDialog";

const agent: DroppedResource = { kind: "agent", id: "agent-1", name: "Refund Agent", createdBySession: true, deployment: "deployed" };
const workflow: DroppedResource = { kind: "workflow", id: "flow-1", name: "Order Flow", createdBySession: true, deployment: "undeploy_failed" };

const removed = (r: DroppedResource): RemovalResult => ({ kind: r.kind, id: r.id, name: r.name, status: "removed" });
const failed = (r: DroppedResource, message = "undeploy failed; see server logs for details"): RemovalResult => ({ kind: r.kind, id: r.id, name: r.name, status: "failed", error: { message, errorId: "err-1", httpStatus: 502 } });

describe("RemoveDroppedDialog", () => {
    let diagnostics: ReturnType<typeof captureRadixDialogDiagnostics>;

    beforeEach(() => {
        diagnostics = captureRadixDialogDiagnostics();
    });
    afterEach(() => {
        diagnostics.restore();
        localStorage.clear();
    });

    test("is closed until it has resources to remove", () => {
        render(<RemoveDroppedDialog snapshot={null} onRemove={vi.fn()} onClose={vi.fn()} />);
        expect(screen.queryByRole("dialog")).toBeNull();
    });

    test("lists every resource by name and kind, with whether it is deployed", () => {
        render(<RemoveDroppedDialog snapshot={[agent, workflow]} onRemove={vi.fn()} onClose={vi.fn()} />);

        const dialog = screen.getByRole("dialog", { name: /Remove these 2 from the platform/, description: /undeploys what is running and deletes them/ });
        expect(within(dialog).getByText("Refund Agent")).toBeInTheDocument();
        expect(within(dialog).getByText("(agent)")).toBeInTheDocument();
        expect(within(dialog).getByText("Order Flow")).toBeInTheDocument();
        expect(within(dialog).getByText("(workflow)")).toBeInTheDocument();
        expect(within(dialog).getByText(/Still deployed and running/)).toBeInTheDocument();
        expect(within(dialog).getByText(/Its undeploy failed, so it is still running/)).toBeInTheDocument();
        expect(diagnostics.messages()).toEqual([]);
    });

    test("puts focus on Cancel, so Enter does not remove anything", () => {
        render(<RemoveDroppedDialog snapshot={[agent]} onRemove={vi.fn()} onClose={vi.fn()} />);
        expect(screen.getByRole("button", { name: "Cancel" })).toHaveFocus();
    });

    test("removes nothing until the user confirms, and cancelling removes nothing", async () => {
        const onRemove = vi.fn();
        const onClose = vi.fn();
        render(<RemoveDroppedDialog snapshot={[agent]} onRemove={onRemove} onClose={onClose} />);

        await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

        expect(onRemove).not.toHaveBeenCalled();
        expect(onClose).toHaveBeenCalled();
    });

    test("is not a place to opt out of asking: it offers no do-not-show-again and ignores that preference", () => {
        localStorage.setItem("sam_builder_skip_activate_confirm", "true");
        localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
        localStorage.setItem("sam_builder_skip_apply_changes_confirm", "true");
        render(<RemoveDroppedDialog snapshot={[agent]} onRemove={vi.fn()} onClose={vi.fn()} />);

        expect(screen.getByRole("dialog")).toBeInTheDocument();
        expect(screen.queryByText(/do not show me this again/i)).toBeNull();
        expect(screen.queryByRole("checkbox")).toBeNull();
    });

    test("removes exactly the resources it lists, and nothing else", async () => {
        const onRemove = vi.fn().mockResolvedValue([removed(agent), removed(workflow)]);
        render(<RemoveDroppedDialog snapshot={[agent, workflow]} onRemove={onRemove} onClose={vi.fn()} />);

        await userEvent.click(screen.getByRole("button", { name: "Remove 2" }));

        await waitFor(() => expect(onRemove).toHaveBeenCalledTimes(1));
        expect(onRemove).toHaveBeenCalledWith([agent, workflow]);
        expect(await screen.findAllByText("Removed")).toHaveLength(2);
        expect(screen.getByRole("button", { name: "Close" })).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /Remove|Retry/ })).toBeNull();
    });

    test("a failed removal stays on screen against its resource, with the reason", async () => {
        const onRemove = vi.fn().mockResolvedValue([failed(agent)]);
        render(<RemoveDroppedDialog snapshot={[agent]} onRemove={onRemove} onClose={vi.fn()} />);

        await userEvent.click(screen.getByRole("button", { name: "Remove" }));

        const alert = await screen.findByRole("alert");
        expect(alert).toHaveTextContent("Could not remove it: undeploy failed; see server logs for details");
        expect(screen.getByText("Refund Agent")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Retry removal" })).toBeInTheDocument();
    });

    test("Retry removes what is still there again, not what already went", async () => {
        const onRemove = vi
            .fn()
            .mockResolvedValueOnce([removed(agent), failed(workflow)])
            .mockResolvedValueOnce([removed(workflow)]);
        render(<RemoveDroppedDialog snapshot={[agent, workflow]} onRemove={onRemove} onClose={vi.fn()} />);

        await userEvent.click(screen.getByRole("button", { name: "Remove 2" }));
        await userEvent.click(await screen.findByRole("button", { name: "Retry removal" }));

        await waitFor(() => expect(onRemove).toHaveBeenCalledTimes(2));
        expect(onRemove).toHaveBeenNthCalledWith(1, [agent, workflow]);
        expect(onRemove).toHaveBeenNthCalledWith(2, [workflow]);
        await waitFor(() => expect(screen.getAllByText("Removed")).toHaveLength(2));
        expect(screen.queryByRole("alert")).toBeNull();
    });

    test("a request that fails outright says so and can be retried", async () => {
        const onRemove = vi
            .fn()
            .mockRejectedValueOnce(new Error("network down"))
            .mockResolvedValueOnce([removed(agent)]);
        render(<RemoveDroppedDialog snapshot={[agent]} onRemove={onRemove} onClose={vi.fn()} />);

        await userEvent.click(screen.getByRole("button", { name: "Remove" }));
        expect(await screen.findByRole("alert")).toHaveTextContent("network down");

        await userEvent.click(screen.getByRole("button", { name: "Retry removal" }));
        await waitFor(() => expect(onRemove).toHaveBeenCalledTimes(2));
        expect(await screen.findByText("Removed")).toBeInTheDocument();
    });

    test("a resource that was already gone is not an error", async () => {
        const onRemove = vi.fn().mockResolvedValue([{ kind: "agent", id: agent.id, status: "not_found" } satisfies RemovalResult]);
        render(<RemoveDroppedDialog snapshot={[agent]} onRemove={onRemove} onClose={vi.fn()} />);

        await userEvent.click(screen.getByRole("button", { name: "Remove" }));

        expect(await screen.findByText("Already gone")).toBeInTheDocument();
        expect(screen.queryByRole("alert")).toBeNull();
    });

    test("a refusal is shown as one, with the server's reason", async () => {
        const refused: RemovalResult = { kind: "agent", id: agent.id, status: "refused", error: { message: "this agent was not created by this Builder session", errorId: "e", httpStatus: 403 } };
        const onRemove = vi.fn().mockResolvedValue([refused]);
        render(<RemoveDroppedDialog snapshot={[agent]} onRemove={onRemove} onClose={vi.fn()} />);

        await userEvent.click(screen.getByRole("button", { name: "Remove" }));

        expect(await screen.findByRole("alert")).toHaveTextContent("Not removed: this agent was not created by this Builder session");
    });

    test("cannot be dismissed while a removal is in flight", async () => {
        let finish: (r: RemovalResult[]) => void = () => {};
        const onRemove = vi.fn().mockImplementation(() => new Promise<RemovalResult[]>(resolve => (finish = resolve)));
        const onClose = vi.fn();
        render(<RemoveDroppedDialog snapshot={[agent]} onRemove={onRemove} onClose={onClose} />);

        await userEvent.click(screen.getByRole("button", { name: "Remove" }));
        expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
        await userEvent.keyboard("{Escape}");
        expect(onClose).not.toHaveBeenCalled();

        finish([removed(agent)]);
        expect(await screen.findByText("Removed")).toBeInTheDocument();
    });
});
