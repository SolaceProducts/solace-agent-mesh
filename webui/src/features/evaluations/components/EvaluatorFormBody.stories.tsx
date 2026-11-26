// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, screen, userEvent, within } from "storybook/test";

import type { EvalEvaluator } from "@/types/eval";

import { defaultChoiceScores, emptyEvaluatorFormValue, EvaluatorFormBody, type EvaluatorFormValue } from "@/features/evaluations/components";

import { evaluatorToV2 } from "@/testing/data/evaluationsV2";
import { anthropicModelConfig, typesafeDecisionModelConfig } from "@/testing/data/models";
import { toModelV2 } from "@/testing/data/modelsV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";

function ControlledEvaluatorFormBody({ initialValue, disabled, typeEditable }: { initialValue: EvaluatorFormValue; disabled?: boolean; typeEditable?: boolean }) {
    const [value, setValue] = useState<EvaluatorFormValue>(initialValue);
    return (
        <div className="max-w-3xl">
            <EvaluatorFormBody value={value} onChange={setValue} disabled={disabled} variant="dialog" typeEditable={typeEditable} />
        </div>
    );
}

const baseValue = (promptTemplate: string): EvaluatorFormValue => ({
    ...emptyEvaluatorFormValue(),
    name: "Quality Judge",
    description: "Test evaluator",
    model: "",
    promptTemplate,
    choiceScores: defaultChoiceScores(),
});

const meta = {
    title: "Evaluations/EvaluatorFormBody",
    component: ControlledEvaluatorFormBody,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component:
                    "Form body for creating or editing an LLM-as-judge evaluator. Highlights only the supported template variables ({{Prompt}}, {{Response}}, {{Expected Response}}) inside the criteria textarea — case-insensitive and tolerant of whitespace inside the braces.",
            },
        },
    },
} satisfies Meta<typeof ControlledEvaluatorFormBody>;

export default meta;
type Story = StoryObj<typeof meta>;

const findHighlightMarks = (canvasElement: HTMLElement): HTMLElement[] => Array.from(canvasElement.querySelectorAll("mark")) as HTMLElement[];

export const HighlightsCanonicalTokens: Story = {
    args: {
        initialValue: baseValue("Compare {{Response}} against {{Expected Response}} for the {{Prompt}}."),
    },
    play: async ({ canvasElement }) => {
        const marks = findHighlightMarks(canvasElement);
        const texts = marks.map(m => m.textContent);
        await expect(texts).toEqual(expect.arrayContaining(["{{Prompt}}", "{{Response}}", "{{Expected Response}}"]));
        await expect(marks).toHaveLength(3);
    },
};

export const HighlightsMixedCaseTokens: Story = {
    args: {
        initialValue: baseValue("Lowercase {{response}} and uppercase {{RESPONSE}} and {{EXPECTED RESPONSE}}."),
    },
    play: async ({ canvasElement }) => {
        const marks = findHighlightMarks(canvasElement);
        const texts = marks.map(m => m.textContent);
        await expect(texts).toEqual(expect.arrayContaining(["{{response}}", "{{RESPONSE}}", "{{EXPECTED RESPONSE}}"]));
        await expect(marks).toHaveLength(3);
    },
};

export const HighlightsWhitespacePaddedTokens: Story = {
    args: {
        initialValue: baseValue("{{ Response }} and {{Expected   Response}} both highlight."),
    },
    play: async ({ canvasElement }) => {
        const marks = findHighlightMarks(canvasElement);
        const texts = marks.map(m => m.textContent);
        await expect(texts).toEqual(expect.arrayContaining(["{{ Response }}", "{{Expected   Response}}"]));
        await expect(marks).toHaveLength(2);
    },
};

export const DoesNotHighlightUnsupportedTokens: Story = {
    args: {
        initialValue: baseValue("Supported {{Response}} but {{Foo}} and {{NotResponse}} are ignored."),
    },
    play: async ({ canvasElement }) => {
        const marks = findHighlightMarks(canvasElement);
        const texts = marks.map(m => m.textContent);
        await expect(marks).toHaveLength(1);
        await expect(texts).toEqual(["{{Response}}"]);
    },
};

export const ChipClickInsertsCanonicalToken: Story = {
    args: {
        initialValue: baseValue(""),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const responseChip = canvas.getByRole("button", { name: "{{Response}}" });
        await userEvent.click(responseChip);

        const textarea = canvas.getByPlaceholderText(/Evaluate whether the response/i) as HTMLTextAreaElement;
        await expect(textarea.value).toBe("{{Response}}");

        const marks = findHighlightMarks(canvasElement);
        await expect(marks).toHaveLength(1);
        await expect(marks[0].textContent).toBe("{{Response}}");
    },
};

const decisionHandlers = withDefaults(http.get("*/api/v2/platform/models", () => HttpResponse.json(paginatedResponse([anthropicModelConfig, typesafeDecisionModelConfig].map(toModelV2)))));

export const TypeChoiceHiddenWhenDecisionOff: Story = {
    args: { initialValue: baseValue(""), typeEditable: true },
    parameters: { featureContext: { eval_decision_evaluator: false } },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.queryByRole("radiogroup", { name: "Evaluator Type" })).toBeNull();
        await expect(canvas.getByTestId("evaluatorTypeReadOnly")).toHaveTextContent("LLM as a Judge");
    },
};

export const DecisionModelEvaluator: Story = {
    args: { initialValue: baseValue(""), typeEditable: true },
    parameters: { featureContext: { eval_decision_evaluator: true }, msw: { handlers: decisionHandlers } },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await userEvent.click(canvas.getByRole("radio", { name: /Decision Model as a Judge/ }));
        await expect(await canvas.findByTestId("decisionEvaluatorFields")).toBeInTheDocument();
        await expect(canvas.queryByPlaceholderText(/Evaluate whether the response/i)).toBeNull();
        await expect(canvas.getByRole("textbox", { name: "Option 1" })).toHaveValue("fail");
        await expect(canvas.getByRole("checkbox", { name: "Expected response" })).toBeChecked();
        await expect(canvas.getByRole("checkbox", { name: "Response" })).toBeDisabled();
        await expect(canvas.getByRole("checkbox", { name: /Transcript/ })).not.toBeChecked();
        await expect(canvas.getByRole("checkbox", { name: "Artifacts (files given to the agent, the expected file, and files it produced)" })).not.toBeChecked();
        await expect(canvas.getByText(/Text files are sent with their content, and other files by name and type only\./)).toBeInTheDocument();
        const questionType = canvas.getByRole("combobox", { name: "Question Type" });
        await expect(questionType).toHaveTextContent("Choice");
        await userEvent.click(questionType);
        await expect(await screen.findByRole("option", { name: /^Score/ })).toHaveTextContent("Rates on ordered levels; the lowest scores 0% and the highest 100%.");
        await userEvent.click(screen.getByRole("option", { name: /^Score/ }));
        await expect(canvas.getByRole("textbox", { name: "Level 1" })).toHaveValue("poor");
        await userEvent.click(questionType);
        await userEvent.click(await screen.findByRole("option", { name: /^Boolean/ }));
        await expect(canvas.getByRole("textbox", { name: "True description" })).toBeInTheDocument();
    },
};

export const EditingDecisionEvaluator: Story = {
    args: {
        initialValue: {
            ...baseValue(""),
            type: "decision",
            decision: { ...emptyEvaluatorFormValue().decision, model: typesafeDecisionModelConfig.id, instructions: "Is the response grounded in the sources?", escalateToEvaluatorId: "", minConfidencePercent: "" },
        },
    },
    parameters: { featureContext: { eval_decision_evaluator: true }, msw: { handlers: decisionHandlers } },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.getByTestId("evaluatorTypeReadOnly")).toHaveTextContent("Decision Model as a Judge");
        await expect(await canvas.findByText("jev")).toBeInTheDocument();
    },
};

const escalationJudge: EvalEvaluator = {
    id: "ev-judge",
    name: "Answer Relevance",
    description: "Scores how well the response addresses the question.",
    type: "llm_judge",
    isDefault: false,
    promptTemplate: "grade this",
    experimentCount: 0,
    createdBy: "alice@example.com",
    updatedBy: "alice@example.com",
    createdAt: "2026-03-01T10:00:00Z",
    updatedAt: "2026-03-15T12:00:00Z",
};

const allFieldsHandlers = withDefaults(
    http.get("*/api/v2/platform/models", () => HttpResponse.json(paginatedResponse([anthropicModelConfig, typesafeDecisionModelConfig].map(toModelV2)))),
    http.get("*/api/v2/platform/evaluations/evaluators", () => HttpResponse.json(paginatedResponse([evaluatorToV2(escalationJudge)])))
);

export const DecisionEvaluatorAllFields: Story = {
    args: {
        initialValue: {
            ...baseValue(""),
            type: "decision",
            decision: {
                ...emptyEvaluatorFormValue().decision,
                model: typesafeDecisionModelConfig.id,
                instructions: "Is the response grounded in the sources?",
                escalateToEvaluatorId: escalationJudge.id,
                minConfidencePercent: "80",
            },
        },
        typeEditable: true,
    },
    parameters: { featureContext: { eval_decision_evaluator: true }, msw: { handlers: allFieldsHandlers } },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.getByRole("combobox", { name: "Question Type" })).toHaveTextContent("Choice");
        await expect(await canvas.findByText(escalationJudge.name)).toBeInTheDocument();
        await expect(canvas.getByRole("spinbutton", { name: "Minimum confidence" })).toHaveValue(80);
    },
};
