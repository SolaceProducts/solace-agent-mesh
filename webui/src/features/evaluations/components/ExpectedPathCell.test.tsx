// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import { userEvent } from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { Table, TableBody } from "@/ui/table";
import type { EvalExample } from "@/types/eval";

import { formatExpectedPath, parseExpectedFactsText, parseExpectedPathText } from "../utils";

import { explainsItsScore, reasoningHeading } from "./decisionScore";
import { ExampleEditorRow } from "./ExampleEditorRows";

const example = {
    id: "ex-1",
    datasetId: "ds-1",
    sequenceNumber: 1,
    prompt: "refund O-1",
    expectedPath: { nodes: { approve: "succeeded" } },
    createdAt: "2026-01-01T00:00:00.000Z",
    updatedAt: "2026-01-01T00:00:00.000Z",
} as EvalExample;

function renderRow(onExpectedPathUpdate = vi.fn().mockResolvedValue(undefined)) {
    render(
        <Table>
            <TableBody>
                <ExampleEditorRow
                    example={example}
                    position={1}
                    onInlineUpdate={vi.fn()}
                    showInputData
                    onInputDataUpdate={vi.fn()}
                    onExpectedPathUpdate={onExpectedPathUpdate}
                    onUploadArtifact={vi.fn()}
                    onRemoveArtifact={vi.fn()}
                    onDownloadArtifact={vi.fn()}
                    onDelete={vi.fn()}
                />
            </TableBody>
        </Table>
    );
    return onExpectedPathUpdate;
}

describe("Workflow Path reasoning", () => {
    test("is shown alongside judges', unlike other heuristics", () => {
        expect(explainsItsScore({ type: "heuristic", heuristicType: "workflow_path" })).toBe(true);
        expect(explainsItsScore({ type: "llm_judge" })).toBe(true);
        expect(explainsItsScore({ type: "heuristic", heuristicType: "levenshtein" })).toBe(false);
        expect(reasoningHeading([{ type: "llm_judge" }, { type: "decision" }])).toBe("Judge Reasoning");
        expect(reasoningHeading([{ type: "llm_judge" }, { type: "heuristic" }])).toBe("Reasoning");
    });
});

describe("expected facts", () => {
    test("parse the lists the Output Facts evaluator checks", () => {
        expect(parseExpectedFactsText("")).toEqual({});
        expect(parseExpectedFactsText('{"required":["P-1"],"forbidden":["refund"]}').value).toEqual({ required: ["P-1"], forbidden: ["refund"] });
        expect(parseExpectedFactsText('{"required":[" "]}').error).toMatch(/non-blank strings/);
        expect(parseExpectedFactsText('{"require":["a"]}').error).toMatch(/not "require"/);
        expect(parseExpectedFactsText('{"required":[]}').error).toMatch(/at least one/);
        expect(explainsItsScore({ type: "heuristic", heuristicType: "output_facts" })).toBe(true);
    });
});

describe("expected path", () => {
    test("parses the assertions the Workflow Path evaluator knows", () => {
        expect(parseExpectedPathText(" ")).toEqual({});
        expect(parseExpectedPathText('{"nodes":{"a":"skipped"},"tools":{"t":"not_called"}}').value).toEqual({ nodes: { a: "skipped" }, tools: { t: "not_called" } });
        expect(parseExpectedPathText('{"nodes":{"a":"done"}}').error).toMatch(/Node "a" must be one of/);
        expect(parseExpectedPathText('{"tools":{"t":true}}').error).toMatch(/Tool "t" must be one of/);
        expect(parseExpectedPathText('{"node":{"a":"ran"}}').error).toMatch(/not "node"/);
        expect(parseExpectedPathText("{}").error).toMatch(/at least one/);
        expect(parseExpectedPathText("[1]").error).toMatch(/JSON object/);
        expect(parseExpectedPathText("{oops").error).toMatch(/not valid JSON/);
        expect(formatExpectedPath(null)).toBe("");
    });

    test("an example's path is edited inline and saved", async () => {
        const save = renderRow();
        await userEvent.click(screen.getByText('{"nodes":{"approve":"succeeded"}}'));
        const box = screen.getByDisplayValue('{"nodes":{"approve":"succeeded"}}');
        await userEvent.clear(box);
        await userEvent.type(box, '{{"tools":{{"issue_refund":"called"}}{Enter}');

        await waitFor(() => expect(save).toHaveBeenCalledWith("ex-1", { tools: { issue_refund: "called" } }));
    });

    test("a bad assertion stays on screen and is not saved", async () => {
        const save = renderRow();
        await userEvent.click(screen.getByText('{"nodes":{"approve":"succeeded"}}'));
        const box = screen.getByDisplayValue('{"nodes":{"approve":"succeeded"}}');
        await userEvent.clear(box);
        await userEvent.type(box, '{{"nodes":{{"approve":"maybe"}}{Enter}');

        expect(await screen.findByText(/Node "approve" must be one of/)).toBeTruthy();
        expect(save).not.toHaveBeenCalled();
    });
});
