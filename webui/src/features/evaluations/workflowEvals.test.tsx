// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { render, renderHook, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { describe, expect, test, vi } from "vitest";

import { Table, TableBody } from "@/ui/table";
import type { EvalExample, EvalRunResult } from "@/types/eval";

import { MockUserProvider } from "@/testing/providers/MockUserProvider";
import { StoryProvider } from "@/testing/providers/StoryProvider";

type QueryStub = { data?: unknown[]; isLoading?: boolean; isError?: boolean };
const mockWorkflows = vi.fn((_options?: { enabled?: boolean }): QueryStub => ({ data: [] }));
const mockCards = vi.fn((): QueryStub => ({ data: [] }));
vi.mock("@/api/workflows", () => ({ useWorkflowsQuery: (options?: { enabled?: boolean }) => mockWorkflows(options) }));
vi.mock("@/api/agent-cards", () => ({ useAgentCards: () => mockCards() }));

import { DatasetExamplesTable } from "./components/DatasetExamplesTable";
import { EvaluatorTile } from "./components/EvaluatorTile";
import { ExampleEditorRow, InputDataCell, NewExampleRow } from "./components/ExampleEditorRows";
import { aggregateCell } from "./components/ExamplesTable";
import { ExamplesTableHeader } from "./components/ExamplesTableHeader";
import { ScoreCellContent } from "./components/ScoreCellContent";
import { TargetTypeIcon, WorkflowEarlyAccessBadge } from "./components/TargetTypeIcon";
import { TrialResponse as ResponseBlock } from "./components/TrialResponse";
import { TrialOutcomeNote, WorkflowTrialDetails } from "./components/WorkflowTrialDetails";
import { useWorkflowOptions } from "./hooks/useWorkflowOptions";
import {
    agentSectionTitle,
    cellStateOf,
    exampleLabel,
    exampleProblemsOf,
    exampleProblemsSummary,
    examplesDescription,
    experimentTargetName,
    failureKindLabel,
    formatInputData,
    isWorkflowExperiment,
    isWorkflowNotDeployed,
    parseInputDataText,
    splitWorkflowPrompt,
    trialOutcomeText,
    trialProblem,
} from "./utils";

describe("workflow experiment helpers", () => {
    test("name the target of either kind", () => {
        expect(experimentTargetName({ targetType: "workflow", targetAgent: "", targetWorkflow: "order_flow" })).toBe("order_flow");
        expect(experimentTargetName({ targetAgent: "OrderDesk" })).toBe("OrderDesk");
        expect(isWorkflowExperiment({ targetType: "agent" })).toBe(false);
        expect(isWorkflowExperiment(null)).toBe(false);
    });

    test("label failure kinds, passing an unknown one through", () => {
        expect(failureKindLabel("paused_for_input")).toBe("Stopped at approval step");
        expect(failureKindLabel("something_new")).toBe("something_new");
        expect(failureKindLabel(undefined)).toBeUndefined();
    });

    test("parse an example's input", () => {
        expect(parseInputDataText("  ")).toEqual({});
        expect(parseInputDataText('{"orderId":"O-1"}').value).toEqual({ orderId: "O-1" });
        expect(parseInputDataText("[1]").error).toMatch(/JSON object/);
        expect(parseInputDataText("null").error).toMatch(/JSON object/);
        expect(parseInputDataText("{oops").error).toMatch(/not valid JSON/);
        expect(parseInputDataText('{"account":12345678901234567890}').error).toBe('The number 12345678901234567890 cannot be kept exactly. Send it as a string instead, like "12345678901234567890".');
        expect(parseInputDataText('{"id":9007199254740993}').error).toMatch(/9007199254740993 cannot be kept exactly/);
        expect(parseInputDataText('{"a":1E400}').error).toMatch(/1E400 cannot be kept exactly/);
        expect(parseInputDataText('{"a":0.12345678901234567}').error).toMatch(/cannot be kept exactly/);
        expect(parseInputDataText('{"price":10.0,"qty":1e2,"note":"12345678901234567890","n":-9007199254740991}').value).toEqual({ price: 10, qty: 100, note: "12345678901234567890", n: -9007199254740991 });
        expect(formatInputData({ a: 1 })).toBe('{"a":1}');
        expect(formatInputData(null)).toBe("");
    });

    test("flag a workflow that is not deployed", () => {
        expect(isWorkflowNotDeployed({ deploymentStatus: "not_deployed" })).toBe(true);
        expect(isWorkflowNotDeployed({ deploymentStatus: "deployed" })).toBe(false);
        expect(isWorkflowNotDeployed({})).toBe(false);
        expect(isWorkflowNotDeployed(undefined)).toBe(false);
    });
});

describe("useWorkflowOptions", () => {
    const withUser =
        (canReadWorkflows = true) =>
        ({ children }: { readonly children: ReactNode }) => <MockUserProvider mockValues={{ userHasCapability: () => canReadWorkflows }}>{children}</MockUserProvider>;

    test("offers platform workflows and YAML-only workflow cards once each", () => {
        mockWorkflows.mockReturnValue({ data: [{ id: "1", name: "order_flow", displayName: "Order Flow", deploymentStatus: "deployed" }] });
        mockCards.mockReturnValue({
            data: [
                { name: "workflow_1", displayName: "Order Flow", isWorkflow: true },
                { name: "yaml_flow", displayName: "", isWorkflow: true },
                { name: "Orchestrator", displayName: "Orchestrator", isWorkflow: false },
                { name: "ephemeral_flow", isWorkflow: true, isEphemeral: true },
            ],
        });

        const { result } = renderHook(() => useWorkflowOptions(), { wrapper: withUser() });

        expect(result.current.options.map(o => o.name)).toEqual(["order_flow", "yaml_flow"]);
        expect(result.current.options[1].displayName).toBe("yaml_flow");
        expect(result.current).toMatchObject({ isLoading: false, isError: false });
    });

    test("keeps a YAML workflow that shares a platform workflow's display name, and drops the platform one's card by its wire name", () => {
        mockWorkflows.mockReturnValue({ data: [{ id: "1a2b", name: "order_flow", displayName: "Orders", deploymentStatus: "deployed" }] });
        mockCards.mockReturnValue({
            data: [
                { name: "workflow_1a2b", displayName: "Orders", isWorkflow: true },
                { name: "yaml_orders", displayName: "Orders", isWorkflow: true },
            ],
        });

        const { result } = renderHook(() => useWorkflowOptions(), { wrapper: withUser() });

        expect(result.current.options.map(o => o.name).sort()).toEqual(["order_flow", "yaml_orders"]);
    });

    test("is loading while either list loads", () => {
        mockWorkflows.mockReturnValue({ isLoading: true });
        mockCards.mockReturnValue({ data: [] });
        expect(renderHook(() => useWorkflowOptions(), { wrapper: withUser() }).result.current).toMatchObject({ options: [], isLoading: true, isError: false });

        mockWorkflows.mockReturnValue({ data: [] });
        mockCards.mockReturnValue({ isLoading: true });
        expect(renderHook(() => useWorkflowOptions(), { wrapper: withUser() }).result.current.isLoading).toBe(true);
    });

    test("reports a failed workflow list", () => {
        mockWorkflows.mockReturnValue({ isError: true });
        mockCards.mockReturnValue({ data: [] });
        expect(renderHook(() => useWorkflowOptions(), { wrapper: withUser() }).result.current).toMatchObject({ isLoading: false, isError: true });
    });

    test("when not enabled, does not request the workflow list and reports neither loading nor failure", () => {
        mockWorkflows.mockReturnValue({ isLoading: true, isError: true });
        mockCards.mockReturnValue({ isLoading: true });

        const { result } = renderHook(() => useWorkflowOptions(false), { wrapper: withUser() });

        expect(mockWorkflows).toHaveBeenLastCalledWith({ enabled: false });
        expect(result.current).toMatchObject({ isLoading: false, isError: false });
    });

    test("a user who may not read workflows gets the announced workflows only, and no failure", () => {
        mockWorkflows.mockReturnValue({ isError: true });
        mockCards.mockReturnValue({ data: [{ name: "yaml_flow", displayName: "YAML Flow", isWorkflow: true }] });

        const { result } = renderHook(() => useWorkflowOptions(), { wrapper: withUser(false) });

        expect(mockWorkflows).toHaveBeenLastCalledWith({ enabled: false });
        expect(result.current).toMatchObject({ readable: false, isLoading: false, isError: false });
        expect(result.current.options.map(o => o.name)).toEqual(["yaml_flow"]);
    });

    test("carries each platform workflow's deployment state", () => {
        mockWorkflows.mockReturnValue({
            data: [
                { id: "1", name: "live_flow", displayName: "Live", deploymentStatus: "deployed" },
                { id: "2", name: "idle_flow", displayName: "Idle", deploymentStatus: "not_deployed" },
            ],
        });
        mockCards.mockReturnValue({ data: [] });

        const { result } = renderHook(() => useWorkflowOptions(), { wrapper: withUser() });

        expect(result.current.options.map(o => [o.name, o.deploymentStatus])).toEqual([
            ["idle_flow", "not_deployed"],
            ["live_flow", "deployed"],
        ]);
    });
});

function trial(overrides: Partial<EvalRunResult>): EvalRunResult {
    return { id: "r1", runId: "run", exampleId: "ex", trialIndex: 0, scores: [], createdAt: "", ...overrides };
}

describe("WorkflowTrialDetails", () => {
    test("shows tokens and cost overall and per node", () => {
        render(
            <WorkflowTrialDetails
                result={trial({
                    cost: "0.0123",
                    tokenBreakdown: {
                        incomplete: false,
                        byNode: [{ nodeId: "extract", models: ["claude"], inputTokens: 1200, outputTokens: 30, cachedInputTokens: 0, reasoningTokens: 0, totalTokens: 1230 }],
                        byModel: { claude: { inputTokens: 1200, outputTokens: 30, cachedInputTokens: 0, reasoningTokens: 0, totalTokens: 1230 } },
                    },
                })}
            />
        );

        expect(screen.getByText("0.0123")).toBeInTheDocument();
        expect(screen.getByText("extract")).toBeInTheDocument();
        expect(screen.getAllByText("1,230")).toHaveLength(2);
        const [, row] = screen.getAllByRole("row");
        expect(Array.from(row.querySelectorAll("td")).map(c => c.textContent)).toEqual(["extract", "claude", "1,200", "30", "1,230"]);
        expect(screen.queryByTestId("tokensLowerBound")).not.toBeInTheDocument();
    });

    test("lists a decision step's tokens in their own row, so the rows add up to the total, and shortens the cost", () => {
        const counts = (input: number, output: number) => ({ inputTokens: input, outputTokens: output, cachedInputTokens: 0, reasoningTokens: 0, totalTokens: input + output });
        render(
            <WorkflowTrialDetails
                result={trial({
                    cost: "0.0142000000",
                    tokenBreakdown: {
                        incomplete: false,
                        byNode: [{ nodeId: "extract", models: ["claude"], ...counts(100, 10) }],
                        unattributed: counts(40, 2),
                        byModel: { claude: counts(100, 10), gemini: counts(40, 2) },
                    },
                })}
            />
        );

        expect(screen.getByText("0.0142")).toBeInTheDocument();
        expect(screen.getByTestId("unattributedTokens")).toHaveTextContent("42");
        expect(screen.getByText("152")).toBeInTheDocument();
    });

    test("says the token total is a lower bound when the count is incomplete", () => {
        render(<WorkflowTrialDetails result={trial({ tokenBreakdown: { incomplete: true, byNode: [], byModel: {} } })} />);
        expect(screen.getByTestId("tokensLowerBound")).toBeInTheDocument();
    });

    test("says when the cost is unknown, and renders nothing for an agent trial", () => {
        const { container, rerender } = render(<WorkflowTrialDetails result={trial({ tokenBreakdown: { incomplete: false, byNode: [], byModel: {} } })} />);
        expect(screen.getByText("unknown")).toBeInTheDocument();

        rerender(<WorkflowTrialDetails result={trial({})} />);
        expect(container).toBeEmptyDOMElement();
    });
});

const example: EvalExample = { id: "ex-1", datasetId: "ds", sequenceNumber: 1, prompt: "", inputData: { orderId: "O-1" }, createdAt: "", updatedAt: "" };

function renderRow(onInputDataUpdate = vi.fn(() => Promise.resolve())) {
    render(
        <Table>
            <TableBody>
                <ExampleEditorRow
                    example={example}
                    position={1}
                    onInlineUpdate={vi.fn(() => Promise.resolve())}
                    onInputDataUpdate={onInputDataUpdate}
                    onUploadArtifact={vi.fn(() => Promise.resolve())}
                    onRemoveArtifact={vi.fn(() => Promise.resolve())}
                    onDownloadArtifact={vi.fn(() => Promise.resolve())}
                    onDelete={vi.fn()}
                />
            </TableBody>
        </Table>
    );
    return onInputDataUpdate;
}

describe("trials that ended early or lost a branch", () => {
    const workflowTrial = (overrides: Partial<EvalRunResult>) => trial({ targetType: "workflow", ...overrides });

    test("a workflow trial that ended early reads as ended, not as an error", () => {
        expect(trialProblem(workflowTrial({ failureKind: "node_failed", errorMessage: "lookup failed" }))).toBe("ended");
        expect(trialProblem(workflowTrial({ failureKind: "partial_failure" }))).toBeNull();
        expect(trialProblem(trial({ errorMessage: "Agent error: boom" }))).toBe("error");
        expect(trialProblem(trial({ failureKind: "auth_required", errorMessage: "connect tools" }))).toBe("error");
        expect(trialProblem(undefined)).toBeNull();
    });

    test("cells show the score, ended early, err or pending", () => {
        expect(cellStateOf(undefined, undefined, null)).toEqual({ state: "pending" });
        expect(cellStateOf(workflowTrial({}), undefined, "ended")).toEqual({ state: "ended" });
        expect(cellStateOf(trial({}), undefined, "error")).toEqual({ state: "err" });
        expect(cellStateOf(workflowTrial({ failureKind: "partial_failure" }), 1, null)).toEqual({ state: "score", value: 1 });
        expect(cellStateOf(trial({}), undefined, null)).toEqual({ state: "missing" });
        render(<ScoreCellContent cell={{ state: "ended" }} />);
        expect(screen.getByText("ended early")).toBeInTheDocument();
    });

    test("an example whose trials all ended early aggregates as ended; a graded one keeps its score", () => {
        const ended = workflowTrial({ failureKind: "paused_for_input", errorMessage: "asked a person" });
        const graded = workflowTrial({ failureKind: "partial_failure", scores: [{ id: "s", evaluatorId: "ev", score: 1 } as EvalRunResult["scores"][number]] });
        expect(aggregateCell([ended, ended], "ev")).toEqual({ state: "ended" });
        expect(aggregateCell([ended, graded], "ev")).toEqual({ state: "score", value: 1 });
        expect(aggregateCell([trial({ errorMessage: "Agent error" }), graded], "ev")).toEqual({ state: "err" });
    });

    test("the partial-failure note reads as a warning, with no Details when the server sends no message", () => {
        render(<TrialOutcomeNote {...trialOutcomeText("partial_failure", "")} testId="trialPartialFailure" />);
        const note = screen.getByTestId("trialPartialFailure");
        expect(note).toHaveTextContent("Run completed with a failed step");
        expect(note).toHaveTextContent("A step failed, so the workflow completed without that branch. The scores include this run.");
        expect(screen.queryByText("Details")).not.toBeInTheDocument();
        expect(note.className).toContain("warning");
        expect(failureKindLabel("partial_failure")).toBe("Completed with a failed step");
    });

    test("an example's response shows the partial-failure note for a scored trial whose message is null", () => {
        render(<ResponseBlock result={workflowTrial({ failureKind: "partial_failure", errorMessage: undefined, agentResponse: '{"ok":true}' })} />);
        expect(screen.getByTestId("trialPartialFailure")).toBeInTheDocument();
        expect(screen.getByText('{"ok":true}')).toBeInTheDocument();
    });

    test("an example's response shows why a workflow trial ended early", () => {
        render(<ResponseBlock result={workflowTrial({ failureKind: "node_failed", errorMessage: 'Workflow "w" failed: node "lookup" failed: boom' })} />);
        expect(screen.getByTestId("trialEndedEarly")).toHaveTextContent("The lookup step failed.");
    });

    test("an agent trial's error stays the plain error it was, whatever its failure kind", () => {
        render(<ResponseBlock result={trial({ targetType: "agent", failureKind: "auth_required", errorMessage: "Authentication required: connect Jira" })} />);
        expect(screen.queryByTestId("trialEndedEarly")).not.toBeInTheDocument();
        expect(screen.getByText("Authentication required: connect Jira")).toBeInTheDocument();
    });

    test("a failed step reads as an error that names the step and the tool", () => {
        const text = trialOutcomeText("node_failed", 'Workflow "OrderDeskStrict" failed: node "lookup" failed: tool "get_order" error: HTTP 500');
        expect(text).toEqual({
            tone: "error",
            title: "Unable to complete run",
            body: "The lookup step failed because the get_order tool returned an error. This run is not included in the scores. Check that the service this tool connects to is available.",
            details: 'Workflow "OrderDeskStrict" failed: node "lookup" failed: tool "get_order" error: HTTP 500',
        });
        expect(trialOutcomeText("node_failed", 'Workflow "w" failed: node "fan_out" failed: node "lookup" failed: tool "crm" error: boom').body).toBe(
            "The lookup step failed because the crm tool returned an error. This run is not included in the scores. Check that the service this tool connects to is available."
        );
        expect(trialOutcomeText("node_failed", 'Workflow "w" failed: node "lookup" failed: tool "crm" error: connect to node "db-1" refused').body).toBe(
            "The lookup step failed because the crm tool returned an error. This run is not included in the scores. Check that the service this tool connects to is available."
        );
        expect(trialOutcomeText("node_failed", 'Workflow "w" failed: node "s1" failed: the agent said tool "web_search" was slow').body).toBe("The s1 step failed. This run is not included in the scores. Open the details to see the error.");
        expect(trialOutcomeText("node_failed", 'Workflow "w" failed: node "say \\"hi\\"" failed: boom').body).toBe('The say "hi" step failed. This run is not included in the scores. Open the details to see the error.');
        expect(trialOutcomeText("paused_for_input", 'A node asked for human input, which an evaluation run cannot give: approve node "x"?').body).toBe(
            "A step requires approval from a person, and experiment runs cannot wait for approval. This run is not included in the scores. Use examples that do not reach this step."
        );
        expect(trialOutcomeText("node_failed", "it broke").body).toBe("A step failed. This run is not included in the scores. Open the details to see the error.");
        expect(trialOutcomeText("paused_for_input", 'Node "escalate" asked for human input, which an evaluation run cannot give.').body).toBe(
            "The escalate step requires approval from a person, and experiment runs cannot wait for approval. This run is not included in the scores. Use examples that do not reach this step."
        );
        expect(trialOutcomeText("timeout", "The workflow ran past its timeout.")).toEqual({ tone: "error", title: "Unable to complete run: Timed out", body: "The workflow ran past its timeout." });
    });
});

describe("evaluator tile consistency", () => {
    test("shows how consistently the evaluator passed each example when given", () => {
        const { rerender } = render(<EvaluatorTile evaluatorName="Output Facts" scoresByModel={{}} allModels={[]} consistency={{ examples: 3, passedEvery: 1, inconsistent: 2, trials: 3 }} />);
        expect(screen.getByTestId("evaluatorConsistencyLine")).toHaveTextContent("1 of 3 examples passed all runs. 2 examples partially failed.");

        rerender(<EvaluatorTile evaluatorName="Output Facts" scoresByModel={{}} allModels={[]} />);
        expect(screen.queryByTestId("evaluatorConsistencyLine")).not.toBeInTheDocument();
    });
});

describe("workflow Early Access badge", () => {
    test("marks a workflow target and nothing else", () => {
        const { rerender } = render(<WorkflowEarlyAccessBadge targetType="workflow" />);
        expect(screen.getByText("EARLY ACCESS")).toBeInTheDocument();
        rerender(<WorkflowEarlyAccessBadge targetType="agent" />);
        expect(screen.queryByText("EARLY ACCESS")).not.toBeInTheDocument();
    });
});

describe("target type icon", () => {
    const renderIcon = (targetType: "agent" | "workflow", workflowEvals: boolean) =>
        render(
            <StoryProvider flagValues={{ workflow_evals: workflowEvals }}>
                <TargetTypeIcon targetType={targetType} />
            </StoryProvider>
        );

    test("an agent target shows no icon while workflow evaluations are off", () => {
        renderIcon("agent", false);
        expect(screen.queryByTestId("targetTypeIcon")).not.toBeInTheDocument();
    });

    test("an agent target shows the agent icon while workflow evaluations are on", () => {
        renderIcon("agent", true);
        expect(screen.getByTestId("targetTypeIcon")).toHaveAttribute("data-target-type", "agent");
    });

    test("a workflow target always shows the workflow icon", () => {
        renderIcon("workflow", false);
        expect(screen.getByTestId("targetTypeIcon")).toHaveAttribute("data-target-type", "workflow");
    });
});

describe("Connect Tools section titles", () => {
    test("name the workflow nodes, the target, or the delegate", () => {
        expect(agentSectionTitle({ isTarget: false, nodeIds: ["classify"] }, "OrderClassifier")).toBe("Required by the classify step, which calls the OrderClassifier agent");
        expect(agentSectionTitle({ isTarget: false, nodeIds: ["classify", "research/fetch"] }, "Researcher")).toBe("Required by the classify, research/fetch steps, which call the Researcher agent");
        expect(agentSectionTitle({ isTarget: true }, "OrderDesk")).toBe("OrderDesk");
        expect(agentSectionTitle({ isTarget: true }, "OrderFlow", true)).toBe("Required by tool steps in OrderFlow");
        expect(agentSectionTitle({ isTarget: false, nodeIds: [] }, "JiraAgent")).toBe("Delegates to: JiraAgent");
    });
});

describe("save-time example problems", () => {
    test("groups the details by example, in order, and leaves other fields out", () => {
        expect(
            exampleProblemsOf({
                "examples[10].inputData": ["plain message"],
                "examples[2].inputData": ["Validation error at 'action': at '/action': value must be one of 'a', 'b'"],
                "examples[2].inputArtifact": ["a workflow example cannot attach an input file"],
                target_workflow: ["no workflow"],
            })
        ).toEqual([
            { sequenceNumber: 2, position: 2, problems: ['The action field in the input data has a value that this workflow does not accept. Change the value to one of these values: "a", "b".', "a workflow example cannot attach an input file"] },
            { sequenceNumber: 10, position: 10, problems: ["plain message"] },
        ]);
        expect(exampleProblemsOf(undefined)).toEqual([]);
        expect(exampleProblemsOf({ "examples[1].inputData": ["Validation error at 'order': at '/order': missing property 'id'"] })[0].problems).toEqual([
            "The input data does not include the required field order.id. Add this field to the input data.",
        ]);
        expect(exampleProblemsSummary(1, "OrderDesk")).toBe("The input data in 1 example does not match OrderDesk. Correct the examples listed.");
        expect(
            exampleProblemsOf({ "examples[7].inputData": ["Validation error at 'order': at '/order/kind': value must be one of 'a', 'b'"] }, [
                { sequenceNumber: 7, inputData: { order: { kind: 3 } } },
                { sequenceNumber: 2, inputData: null },
            ])
        ).toEqual([{ sequenceNumber: 7, position: 2, problems: ['The order.kind field in the input data is 3, which this workflow does not accept. Change it to one of these values: "a", "b".'] }]);
    });

    test.each([
        ["missing properties 'a', 'b'", "", "The input data does not include the required fields a, b. Add these fields to the input data."],
        ["missing properties 'id', 'kind'", "/order", "The input data does not include the required fields order.id, order.kind. Add these fields to the input data."],
        ["additional properties 'z' not allowed", "", "The input data includes the field z, which this workflow does not accept. Remove it from the input data."],
        ["additional properties 'y', 'z' not allowed", "/order", "The input data includes the fields order.y, order.z, which this workflow does not accept. Remove them from the input data."],
        ["got string, want integer", "/qty", "The qty field in the input data must be an integer, but the example sent a string. Change the value in the input data."],
        ["got string, want object", "", "The input data must be an object, but the example sent a string. Change the value in the input data."],
        ["value must be one of 'don\\'t', 'do', 3", "/choice", 'The choice field in the input data has a value that this workflow does not accept. Change the value to one of these values: "don\'t", "do", 3.'],
        ["value must be 'fixed'", "/mode", 'The mode field in the input data has a value that this workflow does not accept. Change the value to "fixed".'],
        ["value must be one of 1, 2", "/qty", "The qty field in the input data has a value that this workflow does not accept. Change the value to one of these values: 1, 2."],
        ["value must be one of 'y,z', 'x'", "/pick", 'The pick field in the input data has a value that this workflow does not accept. Change the value to one of these values: "y,z", "x".'],
    ])("rewords the validator's %j at %j", (detail, at, expected) => {
        expect(exampleProblemsOf({ "examples[1].inputData": [`Validation error at 'x': at '${at}': ${detail}`] })[0].problems).toEqual([expected]);
    });

    test("a workflow trial's recorded prompt splits into the description and the input", () => {
        expect(splitWorkflowPrompt('Damaged item\n\nInput: {"orderId":"O-1"}')).toEqual({ description: "Damaged item", input: '{"orderId":"O-1"}' });
        expect(splitWorkflowPrompt('{"orderId":"O-1"}')).toEqual({ input: '{"orderId":"O-1"}' });
        expect(splitWorkflowPrompt("Look up order O-1")).toEqual({ description: "Look up order O-1" });
    });

    test("a report names a workflow example by its description, else its input, and an agent example by its prompt", () => {
        const workflowTrial = { targetType: "workflow" } as EvalRunResult;
        expect(exampleLabel({ prompt: 'Damaged item\n\nInput: {"orderId":"O-1"}', perModelTrials: { m: [workflowTrial] } })).toBe("Damaged item");
        expect(exampleLabel({ prompt: '{"orderId":"O-1"}', perModelTrials: { m: [workflowTrial] } })).toBe('{"orderId":"O-1"}');
        expect(exampleLabel({ prompt: "Hello\n\nInput: x", perModelTrials: { m: [{ targetType: "agent" } as EvalRunResult] } })).toBe("Hello\n\nInput: x");
    });

    test("the dataset preview shows an example's input", () => {
        render(<DatasetExamplesTable rows={[{ id: "e1", prompt: "", inputData: { orderId: "R-1" } }]} />);
        expect(screen.getByText('{"orderId":"R-1"}')).toBeInTheDocument();
    });
});

describe("editing an example's JSON", () => {
    test("a parse error stays on screen with the typed text still in the editor", async () => {
        const onSave = vi.fn(() => Promise.resolve());
        render(<InputDataCell value={{ orderId: "O-1" }} onSave={onSave} />);
        await userEvent.click(screen.getByText('{"orderId":"O-1"}'));
        const editor = screen.getByRole("textbox");
        await userEvent.clear(editor);
        await userEvent.type(editor, '{{"orderId": ');
        await userEvent.keyboard("{Enter}");

        expect(onSave).not.toHaveBeenCalled();
        expect(screen.getByRole("textbox")).toHaveValue('{"orderId": ');
        expect(screen.getByText("Input is not valid JSON.")).toBeInTheDocument();
    });

    test("a refused save shows why and keeps the typed text", async () => {
        const onSave = vi.fn(() => Promise.reject(new Error("prompt is required unless the example has inputData")));
        render(<InputDataCell value={{ orderId: "O-1" }} onSave={onSave} />);
        await userEvent.click(screen.getByText('{"orderId":"O-1"}'));
        const editor = screen.getByRole("textbox");
        await userEvent.clear(editor);
        await userEvent.type(editor, '{{"orderId": "O-2"}');
        await userEvent.keyboard("{Enter}");

        await waitFor(() => expect(screen.getByText("prompt is required unless the example has inputData")).toBeInTheDocument());
        expect(onSave).toHaveBeenCalledWith({ orderId: "O-2" });
        expect(screen.getByRole("textbox")).toHaveValue('{"orderId": "O-2"}');
    });
});

describe("read-only example input", () => {
    test("shows the input without offering to edit it", async () => {
        render(<InputDataCell value={{ orderId: "O-1" }} />);
        await userEvent.click(screen.getByText('{"orderId":"O-1"}'));
        expect(screen.queryByDisplayValue('{"orderId":"O-1"}')).not.toBeInTheDocument();
    });

    test("labels the first column for a prompt or an input, which then is not required", () => {
        const { rerender } = render(
            <Table>
                <ExamplesTableHeader withInputData />
            </Table>
        );
        expect(screen.getByText("Prompt or Input")).toBeInTheDocument();
        rerender(
            <Table>
                <ExamplesTableHeader />
            </Table>
        );
        expect(screen.queryByText("Prompt or Input")).not.toBeInTheDocument();
        expect(examplesDescription(true)).toMatch(/JSON input for a workflow/);
        expect(examplesDescription(false)).toMatch(/prompt \(required\)/);
    });
});

describe("example input editor", () => {
    test("saves a parsed input and keeps a parse error on screen", async () => {
        const onSave = renderRow();
        await userEvent.click(screen.getByText('{"orderId":"O-1"}'));
        const editor = screen.getByDisplayValue('{"orderId":"O-1"}');

        await userEvent.clear(editor);
        await userEvent.type(editor, "[[1]");
        await userEvent.tab();
        expect(await screen.findByText(/JSON object/)).toBeInTheDocument();
        expect(onSave).not.toHaveBeenCalled();

        const again = screen.getByDisplayValue("[1]");
        await userEvent.clear(again);
        await userEvent.type(again, '{{"orderId":"O-2"}');
        await userEvent.tab();
        await waitFor(() => expect(onSave).toHaveBeenCalledWith("ex-1", { orderId: "O-2" }));
    });

    test("clears the input when the editor is emptied", async () => {
        const onSave = renderRow();
        await userEvent.click(screen.getByText('{"orderId":"O-1"}'));
        await userEvent.clear(screen.getByDisplayValue('{"orderId":"O-1"}'));
        await userEvent.tab();
        await waitFor(() => expect(onSave).toHaveBeenCalledWith("ex-1", null));
    });

    test("a new row with only an input can be saved", async () => {
        const onSave = vi.fn();
        const onInputDataChange = vi.fn();
        const { rerender } = render(
            <Table>
                <TableBody>
                    <NewExampleRow
                        prompt=""
                        onPromptChange={vi.fn()}
                        inputData=""
                        onInputDataChange={onInputDataChange}
                        expected=""
                        onExpectedChange={vi.fn()}
                        file={null}
                        onFileChange={vi.fn()}
                        expectedFile={null}
                        onExpectedFileChange={vi.fn()}
                        saving={false}
                        error={null}
                        onSave={onSave}
                        onCancel={vi.fn()}
                    />
                </TableBody>
            </Table>
        );
        expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
        await userEvent.type(screen.getByLabelText("Input data (JSON)"), "x");
        expect(onInputDataChange).toHaveBeenCalled();

        rerender(
            <Table>
                <TableBody>
                    <NewExampleRow
                        prompt=""
                        onPromptChange={vi.fn()}
                        inputData='{"a":1}'
                        onInputDataChange={onInputDataChange}
                        expected=""
                        onExpectedChange={vi.fn()}
                        file={null}
                        onFileChange={vi.fn()}
                        expectedFile={null}
                        onExpectedFileChange={vi.fn()}
                        saving={false}
                        error={null}
                        onSave={onSave}
                        onCancel={vi.fn()}
                    />
                </TableBody>
            </Table>
        );
        await userEvent.click(screen.getByRole("button", { name: "Save" }));
        expect(onSave).toHaveBeenCalled();
    });
});
