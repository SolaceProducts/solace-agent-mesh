// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, test, vi } from "vitest";

import { modelsApi } from "@/api/models";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { ModelDeleteDialog } from "./ModelDeleteDialog";

const MODEL_ID = "model-under-test";
const SERVER_ERROR_MESSAGE = "the model store is unavailable";

const agentDependent = { id: "agent-1", name: "Support Bot", type: "agent", deploymentStatus: "deployed" };
const workflowDependent = { id: "wf-1", name: "Invoice Router", type: "workflow", deploymentStatus: "deployed" };

const server = setupServer();

beforeAll(() => server.listen());
afterEach(() => {
    server.resetHandlers();
    vi.restoreAllMocks();
});
afterAll(() => server.close());

interface Dependent {
    id: string;
    name: string;
    type: string;
    deploymentStatus: string;
    mayStillRun?: boolean;
}

const DEPENDENTS_PATH = "*/api/v2/platform/models/:modelId/dependents";
const MODEL_PATH = "*/api/v2/platform/models/:modelId";

const dependentsPage = (dependents: Dependent[]) =>
    HttpResponse.json({
        data: dependents.map(d => ({ type: "modelDependent", key: d.id, name: d.name, dependentType: d.type, deploymentStatus: d.deploymentStatus, mayStillRun: d.mayStillRun ?? false })),
        meta: { pagination: { pageNumber: 1, pageSize: 100, count: dependents.length, totalPages: 1, nextPage: null } },
    });

const dependentsHandler = (dependents: Dependent[]) => http.get(DEPENDENTS_PATH, () => dependentsPage(dependents));

const renderDialog = (onConfirm: () => void | Promise<void> = vi.fn()) => renderWithProviders(<ModelDeleteDialog open onOpenChange={vi.fn()} onConfirm={onConfirm} modelId={MODEL_ID} modelAlias="decider" isLoading={false} />);

const deleteButton = () => screen.getByRole("button", { name: /^delete$/i });

describe("ModelDeleteDialog", () => {
    test("lists workflow dependents like agents: a link each, the un-deploy warning, and the typed confirmation", async () => {
        const openSpy = vi.spyOn(window, "open").mockReturnValue(null);
        const onConfirm = vi.fn();
        server.use(dependentsHandler([workflowDependent]));
        renderDialog(onConfirm);

        const block = await screen.findByTestId("modelDeleteWorkflowDependents");
        expect(within(block).getByText("Affected Workflows (1)")).toBeInTheDocument();
        expect(within(block).getByText(/being used by 1 workflow\. Deleting this model will un-deploy any workflows that are currently deployed/)).toBeInTheDocument();
        expect(screen.queryByText(/Affected Agents/)).not.toBeInTheDocument();

        expect(within(block).getByTestId("modelDeleteWillUndeploy")).toHaveTextContent("Will be undeployed");

        await userEvent.click(within(block).getByRole("button", { name: /Invoice Router/ }));
        expect(openSpy).toHaveBeenCalledWith("#/agent-mesh/workflows/Invoice%20Router", "_blank");

        expect(deleteButton()).toBeDisabled();
        await userEvent.type(screen.getByTestId("modelDeleteConfirmInput"), "decider");
        expect(deleteButton()).toBeEnabled();
        await userEvent.click(deleteButton());
        expect(onConfirm).toHaveBeenCalledOnce();
    });

    test("lists agents and workflows together and still deletes after the typed confirmation", async () => {
        server.use(dependentsHandler([agentDependent, workflowDependent]));
        renderDialog();

        expect(await screen.findByText("Affected Workflows (1)")).toBeInTheDocument();
        expect(screen.getByText("Affected Agents (1)")).toBeInTheDocument();
        expect(screen.getByText(/being used by 1 agent\./)).toBeInTheDocument();
        await userEvent.type(screen.getByTestId("modelDeleteConfirmInput"), "decider");
        expect(deleteButton()).toBeEnabled();
    });

    test("agent-only dependents keep the un-deploy warning and the typed confirmation", async () => {
        const openSpy = vi.spyOn(window, "open").mockReturnValue(null);
        const onConfirm = vi.fn();
        server.use(dependentsHandler([agentDependent]));
        renderDialog(onConfirm);

        expect(await screen.findByText("Affected Agents (1)")).toBeInTheDocument();
        expect(screen.getByText(/Deleting this model will cause these agents to become un-deployed/)).toBeInTheDocument();
        expect(screen.queryByTestId("modelDeleteWorkflowDependents")).not.toBeInTheDocument();

        await userEvent.click(screen.getByRole("button", { name: /Support Bot/ }));
        expect(openSpy).toHaveBeenCalledWith("#/agent-mesh/agent-1", "_blank");

        expect(deleteButton()).toBeDisabled();
        await userEvent.type(screen.getByTestId("modelDeleteConfirmInput"), "decider");
        expect(deleteButton()).toBeEnabled();

        await userEvent.click(deleteButton());
        expect(onConfirm).toHaveBeenCalledOnce();
    });

    test("tags only the deployed dependents as the ones the delete undeploys", async () => {
        server.use(dependentsHandler([workflowDependent, { id: "wf-2", name: "Ticket Triage", type: "workflow", deploymentStatus: "not_deployed" }]));
        renderDialog();

        const block = await screen.findByTestId("modelDeleteWorkflowDependents");
        const tags = within(block).getAllByTestId("modelDeleteWillUndeploy");
        expect(tags).toHaveLength(1);
        expect(tags[0].parentElement).toHaveTextContent("Invoice Router");
    });

    test("enables Delete only when the model's alias is typed, not the word DELETE", async () => {
        server.use(dependentsHandler([]));
        renderDialog();

        const input = await screen.findByTestId("modelDeleteConfirmInput");
        expect(screen.getByText("decider")).toBeInTheDocument();
        await userEvent.type(input, "DELETE");
        expect(deleteButton()).toBeDisabled();
        await userEvent.clear(input);
        await userEvent.type(input, "Decider");
        expect(deleteButton()).toBeDisabled();
        await userEvent.clear(input);
        await userEvent.type(input, "decider");
        expect(deleteButton()).toBeEnabled();
        await userEvent.clear(input);
        await userEvent.type(input, " decider ");
        expect(deleteButton()).toBeEnabled();
    });

    test("keeps Delete disabled when the model has no alias to type", async () => {
        server.use(dependentsHandler([]));
        renderWithProviders(<ModelDeleteDialog open onOpenChange={vi.fn()} onConfirm={vi.fn()} modelId={MODEL_ID} modelAlias="" isLoading={false} />);

        await screen.findByTestId("modelDeleteConfirmInput");
        expect(deleteButton()).toBeDisabled();
    });

    test("labels the confirmation input with the alias to type", async () => {
        server.use(dependentsHandler([]));
        renderDialog();

        expect(await screen.findByLabelText(/Type decider to confirm the deletion/)).toBe(screen.getByTestId("modelDeleteConfirmInput"));
    });

    test("shows the server's message when the delete fails", async () => {
        vi.spyOn(console, "error").mockImplementation(() => {});
        server.use(
            dependentsHandler([]),
            http.delete(MODEL_PATH, () => HttpResponse.json({ message: SERVER_ERROR_MESSAGE, errorId: "err-1" }, { status: 500 }))
        );
        renderDialog(() => modelsApi.delete(MODEL_ID));

        const input = await screen.findByTestId("modelDeleteConfirmInput");
        await userEvent.type(input, "decider");
        await userEvent.click(deleteButton());

        expect(await screen.findByText(SERVER_ERROR_MESSAGE)).toBeInTheDocument();
    });

    test("a partial-cascade 502 puts the error reference on screen", async () => {
        vi.spyOn(console, "error").mockImplementation(() => {});
        const keptMessage = "The model was kept because an agent could not be undeployed.";
        server.use(
            dependentsHandler([]),
            http.delete(MODEL_PATH, () => HttpResponse.json({ message: keptMessage, errorId: "err-kept-7" }, { status: 502, headers: { Location: `/api/v2/platform/models/${MODEL_ID}` } }))
        );
        renderDialog(() => modelsApi.delete(MODEL_ID));

        await userEvent.type(await screen.findByTestId("modelDeleteConfirmInput"), "decider");
        await userEvent.click(deleteButton());

        expect(await screen.findByText(keptMessage)).toBeInTheDocument();
        expect(screen.getByText(/Reference: err-kept-7/)).toBeInTheDocument();
    });

    test("shows the failure before the dependents refetch settles", async () => {
        vi.spyOn(console, "error").mockImplementation(() => {});
        let releaseDependents: (() => void) | undefined;
        let dependentsCalls = 0;
        server.use(
            http.get(DEPENDENTS_PATH, async () => {
                dependentsCalls += 1;
                if (dependentsCalls > 1) await new Promise<void>(resolve => (releaseDependents = resolve));
                return dependentsPage([]);
            }),
            http.delete(MODEL_PATH, () => HttpResponse.json({ message: SERVER_ERROR_MESSAGE, errorId: "err-1" }, { status: 500 }))
        );
        renderDialog(() => modelsApi.delete(MODEL_ID));

        await userEvent.type(await screen.findByTestId("modelDeleteConfirmInput"), "decider");
        await userEvent.click(deleteButton());

        expect(await screen.findByText(SERVER_ERROR_MESSAGE)).toBeInTheDocument();
        releaseDependents?.();
    });

    test("after a failed delete the dependents list reflects what remains", async () => {
        vi.spyOn(console, "error").mockImplementation(() => {});
        let deleteAttempted = false;
        server.use(
            http.get(DEPENDENTS_PATH, () => {
                return dependentsPage(!deleteAttempted ? [agentDependent, { ...agentDependent, id: "agent-2", name: "Billing Bot" }] : [{ ...agentDependent, id: "agent-2", name: "Billing Bot" }]);
            })
        );
        renderDialog(() => {
            deleteAttempted = true;
            return Promise.reject(new Error("could not undeploy Support Bot"));
        });

        expect(await screen.findByRole("button", { name: /Support Bot/ })).toBeInTheDocument();
        await userEvent.type(screen.getByTestId("modelDeleteConfirmInput"), "decider");
        await userEvent.click(deleteButton());

        expect(await screen.findByText("could not undeploy Support Bot")).toBeInTheDocument();
        await waitFor(() => expect(screen.queryByRole("button", { name: /Support Bot/ })).not.toBeInTheDocument());
        expect(screen.getByRole("button", { name: /Billing Bot/ })).toBeInTheDocument();
    });

    test("marks a dependent that may still be running, and only that one", async () => {
        server.use(
            dependentsHandler([
                { ...agentDependent, deploymentStatus: "not_deployed", mayStillRun: true },
                { ...agentDependent, id: "agent-2", name: "Billing Bot", deploymentStatus: "not_deployed" },
                { id: "wf-9", name: "Live Flow", type: "workflow", deploymentStatus: "deployed", mayStillRun: true },
            ])
        );
        renderDialog();

        const flagged = await screen.findByRole("button", { name: /Support Bot/ });
        const unflagged = screen.getByRole("button", { name: /Billing Bot/ });
        expect(within(flagged.parentElement as HTMLElement).getByTestId("modelDeleteMayStillRun")).toHaveTextContent("May still be running");
        expect(within(unflagged.parentElement as HTMLElement).queryByTestId("modelDeleteMayStillRun")).not.toBeInTheDocument();
        const deployed = screen.getByRole("button", { name: /Live Flow/ });
        expect(within(deployed.parentElement as HTMLElement).getByTestId("modelDeleteWillUndeploy")).toBeInTheDocument();
        expect(within(deployed.parentElement as HTMLElement).queryByTestId("modelDeleteMayStillRun")).not.toBeInTheDocument();
    });

    test("a refresh that fails after a failed delete keeps the dependents already listed beside the error", async () => {
        vi.spyOn(console, "error").mockImplementation(() => {});
        let deleteAttempted = false;
        server.use(http.get(DEPENDENTS_PATH, () => (deleteAttempted ? HttpResponse.json({ message: "down" }, { status: 500 }) : dependentsPage([agentDependent]))));
        renderDialog(() => {
            deleteAttempted = true;
            return Promise.reject(new Error("could not undeploy Support Bot"));
        });

        expect(await screen.findByRole("button", { name: /Support Bot/ })).toBeInTheDocument();
        await userEvent.type(screen.getByTestId("modelDeleteConfirmInput"), "decider");
        await userEvent.click(deleteButton());

        expect(await screen.findByText("could not undeploy Support Bot")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: /Support Bot/ })).toBeInTheDocument();
        expect(screen.queryByText(/If any code-based agents are referencing this model/)).not.toBeInTheDocument();
    });
});
