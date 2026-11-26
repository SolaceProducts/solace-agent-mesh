// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, test, vi } from "vitest";

import type { DroppedResource, RemovalResult } from "@/api/builder/applyTypes";

import { type DroppedView, NO_DROPPED } from "./droppedResources";
import { DroppedResourcesNotice } from "./DroppedResourcesNotice";

const created = (over: Partial<DroppedResource> = {}): DroppedResource => ({ kind: "agent", id: "agent-1", name: "Refund Agent", createdBySession: true, deployment: "deployed", ...over });
const view = (resources: DroppedResource[], over: Partial<DroppedView> = {}): DroppedView => ({ status: "ready", resources, checkFailed: false, failures: new Map(), ...over });

afterEach(() => localStorage.clear());

describe("DroppedResourcesNotice", () => {
    test("says nothing when the server says the plan dropped nothing", () => {
        const { container } = render(<DroppedResourcesNotice view={view([])} onRemove={vi.fn()} />);
        expect(container).toBeEmptyDOMElement();
    });

    test("says nothing when there is no answer", () => {
        const { container } = render(<DroppedResourcesNotice view={NO_DROPPED} onRemove={vi.fn()} />);
        expect(container).toBeEmptyDOMElement();
    });

    test("names each dropped resource with its kind and whether it is deployed, from the server's list as given", () => {
        render(<DroppedResourcesNotice view={view([created(), created({ id: "flow-1", kind: "workflow", name: "Order Flow", deployment: "undeploy_failed" })])} onRemove={vi.fn()} />);

        const region = screen.getByRole("region", { name: /2 components this build left in place/ });
        expect(within(region).getByText("Refund Agent")).toBeInTheDocument();
        expect(within(region).getByText(/still deployed and running/)).toBeInTheDocument();
        expect(within(region).getByText("Order Flow")).toBeInTheDocument();
        expect(within(region).getByText(/its undeploy failed, so it is still running/)).toBeInTheDocument();
        expect(within(region).getByText(/Build & Activate does not remove anything/)).toBeInTheDocument();
    });

    test("does not call a resource deployed that the server says is not", () => {
        render(<DroppedResourcesNotice view={view([created({ deployment: "not_deployed" })])} onRemove={vi.fn()} />);
        expect(screen.queryByText(/still deployed and running/)).toBeNull();
        expect(screen.getByText(/saved, but not deployed/)).toBeInTheDocument();
    });

    test("says when the platform could not check, rather than showing an empty answer", () => {
        render(<DroppedResourcesNotice view={view([], { checkFailed: true })} onRemove={vi.fn()} />);
        expect(screen.getByRole("status")).toHaveTextContent(/could not finish checking which components this plan dropped/);
    });

    test("says the list may be incomplete beside what it did find", () => {
        render(<DroppedResourcesNotice view={view([created()], { checkFailed: true })} onRemove={vi.fn()} />);

        expect(screen.getByRole("status")).toHaveTextContent(/may not be listed/);
        expect(screen.getByText("Refund Agent")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Remove Refund Agent…" })).toBeInTheDocument();
    });

    test("offers no removal for a resource the session only updated, and says where to remove it", () => {
        render(<DroppedResourcesNotice view={view([created({ createdBySession: false, name: "Shared Agent" })])} onRemove={vi.fn()} />);

        expect(screen.queryByRole("button", { name: /Remove/ })).toBeNull();
        expect(screen.getByText(/remove it from the Agents page/)).toBeInTheDocument();
    });

    test("offers removal for the created ones only, and the dialog lists exactly those", async () => {
        const mine = created({ id: "mine", name: "Mine" });
        const theirs = created({ id: "theirs", name: "Theirs", createdBySession: false });
        render(<DroppedResourcesNotice view={view([mine, theirs])} onRemove={vi.fn()} />);

        await userEvent.click(screen.getByRole("button", { name: "Remove Mine…" }));

        const dialog = await screen.findByRole("dialog");
        expect(within(dialog).getByText("Mine")).toBeInTheDocument();
        expect(within(dialog).queryByText("Theirs")).toBeNull();
    });

    test("clicking Remove removes nothing: only the dialog's confirmation does, and the saved skip preference does not stand in for it", async () => {
        localStorage.setItem("sam_builder_skip_activate_confirm", "true");
        localStorage.setItem("sam_builder_skip_overwrite_confirm", "true");
        const onRemove = vi.fn().mockResolvedValue([]);
        render(<DroppedResourcesNotice view={view([created()])} onRemove={onRemove} />);

        await userEvent.click(screen.getByRole("button", { name: "Remove Refund Agent…" }));

        expect(await screen.findByRole("dialog")).toBeInTheDocument();
        expect(onRemove).not.toHaveBeenCalled();
    });

    test("removes the set shown when the dialog opened, however the list changes under it", async () => {
        const first = created({ id: "first", name: "First" });
        const second = created({ id: "second", name: "Second" });
        const onRemove = vi.fn().mockResolvedValue([{ kind: "agent", id: "first", status: "removed" } satisfies RemovalResult]);

        let setView: (v: DroppedView) => void = () => {};
        function Host() {
            const [current, set] = useState(view([first]));
            setView = set;
            return <DroppedResourcesNotice view={current} onRemove={onRemove} />;
        }
        render(<Host />);

        await userEvent.click(screen.getByRole("button", { name: "Remove First…" }));
        await screen.findByRole("dialog");
        setView(view([first, second]));
        await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Remove" }));

        await waitFor(() => expect(onRemove).toHaveBeenCalledTimes(1));
        expect(onRemove).toHaveBeenCalledWith([first]);
    });

    test("keeps the dialog and its results when the removal empties the list", async () => {
        const onRemove = vi.fn().mockResolvedValue([{ kind: "agent", id: "agent-1", name: "Refund Agent", status: "removed" } satisfies RemovalResult]);
        let setView: (v: DroppedView) => void = () => {};
        function Host() {
            const [current, set] = useState(view([created()]));
            setView = set;
            return <DroppedResourcesNotice view={current} onRemove={onRemove} />;
        }
        render(<Host />);

        await userEvent.click(screen.getByRole("button", { name: "Remove Refund Agent…" }));
        await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Remove" }));
        setView(view([]));

        expect(await screen.findByText("Removed")).toBeInTheDocument();
        expect(screen.getByRole("dialog")).toBeInTheDocument();
    });

    test("shows a failed removal against its resource while it is still listed, and offers the retry", () => {
        render(<DroppedResourcesNotice view={view([created()], { failures: new Map([["agent-1", "Could not remove it: undeploy failed; see server logs for details"]]) })} onRemove={vi.fn()} />);

        expect(screen.getByRole("alert")).toHaveTextContent("undeploy failed");
        expect(screen.getByRole("button", { name: "Retry removing Refund Agent…" })).toBeInTheDocument();
    });

    test("cannot be asked to remove while the answer is being refreshed", () => {
        render(<DroppedResourcesNotice view={view([created()], { status: "loading" })} onRemove={vi.fn()} />);
        expect(screen.getByRole("button", { name: "Checking…" })).toBeDisabled();
    });
});
