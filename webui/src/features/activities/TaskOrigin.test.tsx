// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, Routes } from "react-router-dom";
import { describe, expect, test } from "vitest";

import { CAPABILITIES } from "@/providers/contexts";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { TaskSubject, TaskTrigger } from "@/types";

import { TaskOrigin } from "./TaskOrigin";

const workflow: TaskSubject = { id: "workflow_01a1", type: "workflow", name: "Dependency update" };
const webhook: TaskTrigger = { kind: "event", channel: "webhook", entrypointId: "ep-1", entrypointName: "github-deps", route: "deps", event: "check_suite.completed", deliveryId: "d-1" };

function renderOrigin(subject: TaskSubject | undefined, trigger: TaskTrigger | undefined, canReadGateways: boolean) {
    return renderWithProviders(
        <Routes>
            <Route path="/" element={<TaskOrigin subject={subject} trigger={trigger} />} />
            <Route path="/entrypoints/:id" element={<p>entrypoint page</p>} />
        </Routes>,
        { userContextValues: { userHasCapability: capability => canReadGateways && capability === CAPABILITIES.readGateways } }
    );
}

describe("TaskOrigin", () => {
    test("says which workflow ran and which webhook, route, event and delivery started it", () => {
        renderOrigin(workflow, webhook, true);

        expect(screen.getByTestId("task-origin-subject")).toHaveTextContent("Dependency update");
        expect(screen.getByTestId("task-origin-source")).toHaveTextContent("webhook");
        expect(screen.getByTestId("task-origin-entrypoint")).toHaveTextContent("github-deps");
        expect(screen.getByTestId("task-origin-details")).toHaveTextContent("route deps · check_suite.completed · delivery d-1");
    });

    test("links the webhook to its entrypoint page for a viewer who can read entrypoints", async () => {
        renderOrigin(workflow, webhook, true);

        await userEvent.setup().click(screen.getByRole("button", { name: "github-deps" }));

        expect(await screen.findByText("entrypoint page")).toBeInTheDocument();
    });

    test("names the webhook without a link for a viewer who cannot read entrypoints", () => {
        renderOrigin(workflow, webhook, false);

        expect(screen.getByTestId("task-origin-entrypoint")).toHaveTextContent("github-deps");
        expect(screen.queryByRole("button", { name: "github-deps" })).toBeNull();
    });

    test("an interactive run shows only its workflow and how it was started", () => {
        renderOrigin(workflow, { kind: "interactive" }, true);

        expect(screen.getByTestId("task-origin")).toHaveTextContent("Dependency update · Started by interactive");
        expect(screen.queryByTestId("task-origin-entrypoint")).toBeNull();
        expect(screen.queryByTestId("task-origin-details")).toBeNull();
    });

    test("names a workflow it cannot resolve by its id", () => {
        renderOrigin({ ...workflow, name: "" }, { kind: "interactive" }, true);

        expect(screen.getByTestId("task-origin-subject")).toHaveTextContent("workflow_01a1");
    });

    test("renders nothing for a row logged before triggers were recorded", () => {
        renderOrigin(undefined, undefined, true);

        expect(screen.queryByTestId("task-origin")).toBeNull();
    });
});
