// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useId, useRef } from "react";
import { HelpCircle, Plus, X } from "lucide-react";

import { useCapabilityBadge } from "@/api/capabilities";

import { InfoTipCard } from "@/common/banners";
import { type RadioOption, RadioOptions } from "@/common/form";
import { PageInlineRow } from "@/common/layout";

import { cn } from "@/utils/cnTailwind";
import { uuid } from "@/utils/uuid";

import { Button } from "@/ui/button";
import { HighlightedTextarea } from "@/ui/highlighted-textarea";
import { Input } from "@/ui/input";
import { LifecycleBadge } from "@/ui/lifecycleBadge";
import { RichTooltip, RichTooltipContent, RichTooltipProvider, RichTooltipTrigger } from "@/ui/rich-tooltip";
import { Textarea } from "@/ui/textarea";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";

import { useIsDecisionEvaluatorEnabled } from "@/hooks";

import { EVAL_DOCS } from "../constants";
import { useDragReorder } from "../hooks/useDragReorder";

import { DecisionEvaluatorFields, FieldError } from "./DecisionEvaluatorFields";
import { DragHandle } from "./DragHandle";
import { errorIds, EVALUATOR_FORM_TYPE_LABELS, type EvaluatorFieldErrors, type EvaluatorFormType, type EvaluatorFormValue, MAX_CHOICES, MIN_CHOICES } from "./evaluatorForm";
import { ModelSelect } from "./ModelSelect";

const DECISION_EVALUATOR_CAPABILITY = "eval_decision_evaluator";

const TYPE_DESCRIPTIONS: Record<EvaluatorFormType, string> = {
    llm_judge: "A chat model grades each response against criteria you write.",
    decision: "A decision model answers one typed question about each response and reports how confident it is.",
};

const CRITERIA_BANNER_KEY = "eval-evaluator-criteria-tips-dismissed";
const SCORES_BANNER_KEY = "eval-evaluator-scores-tips-dismissed";

const CRITERIA_TIPS_HEADER = "Write clear, specific evaluation criteria";
const CRITERIA_TIPS_DESCRIPTION = "Describe the rubric the judge should apply. Reference the agent's input and output using the variables {{Prompt}}, {{Response}}, and {{Expected Response}}.";

const SCORES_TIPS_HEADER = "Map evaluation outcomes to numeric scores";
const SCORES_TIPS_DESCRIPTION = "The judge picks one choice; its numeric score is used to summarize the evaluation. Keep labels short and distinct, and use scores between 0% and 100% for consistent averaging across evaluators.";

function InfoRichTooltip({ content, ariaLabel }: { readonly content: string; readonly ariaLabel: string }) {
    return (
        <RichTooltip>
            <RichTooltipTrigger asChild>
                <button type="button" aria-label={ariaLabel} className="inline-flex items-center border-0 bg-transparent p-0">
                    <HelpCircle className="size-3.5 text-(--secondary-text-wMain)" />
                </button>
            </RichTooltipTrigger>
            <RichTooltipContent className="max-w-xs bg-(--background-wMain) text-(--primary-text-w10)">{content}</RichTooltipContent>
        </RichTooltip>
    );
}

const newChoiceScoreId = (): string => uuid();

const MIN_SCORES = MIN_CHOICES;
const MAX_SCORES = MAX_CHOICES;

const CRITERIA_VARIABLES: readonly { token: string; description: string }[] = [
    { token: "{{Prompt}}", description: "The original input or question sent to the agent." },
    { token: "{{Response}}", description: "The actual response produced by the agent for this prompt." },
    { token: "{{Expected Response}}", description: "The reference / ground-truth answer from the dataset, if provided." },
    { token: "{{Node Output: <node id>}}", description: "For a workflow, what that node returned in the trial, to grade one node rather than the whole run." },
    { token: "{{Node Input: <node id>}}", description: "For a workflow, what that node was given in the trial." },
] as const;

const SUPPORTED_VARIABLES_PATTERN = /(\{\{\s*(?:(?:Prompt|Response|Expected\s+Response)\s*|Node\s+(?:Output|Input)\s*:[^{}\r\n]*)\}\})/gi;

interface EvaluatorFormBodyProps {
    readonly value: EvaluatorFormValue;
    readonly onChange: (v: EvaluatorFormValue) => void;
    readonly disabled?: boolean;
    readonly error?: string | null;
    readonly variant?: "page" | "dialog";
    readonly fieldErrors?: EvaluatorFieldErrors;
    readonly typeEditable?: boolean;
    readonly evaluatorId?: string;
}

function TypeField({ type, editable, onChange, disabled }: { readonly type: EvaluatorFormType; readonly editable: boolean; readonly onChange: (t: EvaluatorFormType) => void; readonly disabled?: boolean }) {
    const decisionEnabled = useIsDecisionEvaluatorEnabled();
    const badgeText = useCapabilityBadge(DECISION_EVALUATOR_CAPABILITY);
    const badge = badgeText ? <LifecycleBadge>{badgeText}</LifecycleBadge> : undefined;

    if (!editable || !decisionEnabled) {
        return (
            <PageInlineRow label="Type">
                <span className="flex items-center gap-2 text-sm" data-testid="evaluatorTypeReadOnly">
                    {EVALUATOR_FORM_TYPE_LABELS[type]}
                    {type === "decision" && badge}
                </span>
            </PageInlineRow>
        );
    }
    const options: RadioOption[] = [
        { value: "llm_judge", label: EVALUATOR_FORM_TYPE_LABELS.llm_judge, description: TYPE_DESCRIPTIONS.llm_judge },
        { value: "decision", label: EVALUATOR_FORM_TYPE_LABELS.decision, description: TYPE_DESCRIPTIONS.decision, badge },
    ];
    return (
        <PageInlineRow label="Type" required>
            <RadioOptions ariaLabel="Evaluator Type" orientation="vertical" options={options} value={type} onChange={t => onChange(t as EvaluatorFormType)} disabled={disabled} />
        </PageInlineRow>
    );
}

export function EvaluatorFormBody({ value, onChange, disabled, error, variant = "page", fieldErrors = {}, typeEditable = false, evaluatorId }: EvaluatorFormBodyProps) {
    const patch = (p: Partial<EvaluatorFormValue>) => onChange({ ...value, ...p });
    const isDecision = value.type === "decision";
    const err = errorIds(useId(), fieldErrors);

    const updateChoiceScore = (index: number, field: "choice" | "score", next: string | number) => {
        patch({
            choiceScores: value.choiceScores.map((cs, i) => (i === index ? { ...cs, [field]: next } : cs)),
        });
    };

    const addChoiceScore = () => patch({ choiceScores: [...value.choiceScores, { id: newChoiceScoreId(), choice: "", score: 0 }] });
    const removeChoiceScore = (index: number) => patch({ choiceScores: value.choiceScores.filter((_, i) => i !== index) });

    const canRemoveScore = value.choiceScores.length > MIN_SCORES;
    const canAddScore = value.choiceScores.length < MAX_SCORES;

    const drag = useDragReorder(value.choiceScores, choiceScores => patch({ choiceScores }), disabled);

    const criteriaTextareaRef = useRef<HTMLTextAreaElement | null>(null);

    const insertVariable = (variable: string) => {
        if (disabled) return;
        const el = criteriaTextareaRef.current;
        const current = value.promptTemplate;
        const start = el?.selectionStart ?? current.length;
        const end = el?.selectionEnd ?? current.length;
        const next = current.slice(0, start) + variable + current.slice(end);
        patch({ promptTemplate: next });
        requestAnimationFrame(() => {
            const node = criteriaTextareaRef.current;
            if (!node) return;
            node.focus();
            const caret = start + variable.length;
            node.setSelectionRange(caret, caret);
        });
    };

    const topFields = (
        <div className="flex flex-col gap-7">
            <PageInlineRow label="Name" required>
                <Input value={value.name} onChange={e => patch({ name: e.target.value })} placeholder="e.g. Quality Judge" disabled={disabled} aria-invalid={!!fieldErrors.name} aria-describedby={err.describedBy("name")} />
                <FieldError message={fieldErrors.name} id={err.id("name")} />
            </PageInlineRow>

            <TypeField type={value.type} editable={typeEditable} onChange={type => patch({ type })} disabled={disabled} />
            {fieldErrors.type && <FieldError message={fieldErrors.type} id={err.id("type")} />}

            <PageInlineRow label="Description" required>
                <Textarea
                    value={value.description}
                    onChange={e => patch({ description: e.target.value })}
                    placeholder="Describe what this evaluator assesses"
                    rows={2}
                    disabled={disabled}
                    aria-invalid={!!fieldErrors.description}
                    aria-describedby={err.describedBy("description")}
                />
                <FieldError message={fieldErrors.description} id={err.id("description")} />
            </PageInlineRow>

            {!isDecision && (
                <PageInlineRow label="Model" required>
                    <ModelSelect value={value.model} onChange={id => patch({ model: id })} disabled={disabled} hideLabel clearable={false} />
                    <FieldError message={fieldErrors.model} id={err.id("model")} />
                </PageInlineRow>
            )}
        </div>
    );

    const criteriaField = (
        <PageInlineRow
            label={
                <span className="inline-flex items-center gap-1.5">
                    Criteria
                    <InfoRichTooltip ariaLabel="About criteria" content={CRITERIA_TIPS_DESCRIPTION} />
                </span>
            }
            required
        >
            <HighlightedTextarea
                ref={criteriaTextareaRef}
                value={value.promptTemplate}
                onChange={e => patch({ promptTemplate: e.target.value })}
                placeholder="Evaluate whether the response correctly and completely answers the question..."
                rows={6}
                disabled={disabled}
                aria-describedby={err.describedBy("promptTemplate")}
                highlightPattern={SUPPORTED_VARIABLES_PATTERN}
                highlightClassName="bg-(--info-w20) text-(--info-wMain) rounded-sm"
            />
            <div className="flex flex-wrap items-center gap-2 text-xs">
                <span className="text-(--secondary-text-wMain)">Insert variable:</span>
                {CRITERIA_VARIABLES.map(({ token, description }) => (
                    <Tooltip key={token}>
                        <TooltipTrigger asChild>
                            <button
                                type="button"
                                onClick={() => insertVariable(token)}
                                disabled={disabled}
                                className={cn("rounded-md bg-(--info-w20) px-2 py-0.5 font-mono text-(--info-wMain) transition-colors", disabled ? "cursor-not-allowed opacity-50" : "cursor-pointer hover:bg-(--info-w30)")}
                            >
                                {token}
                            </button>
                        </TooltipTrigger>
                        <TooltipContent side="top" className="max-w-none whitespace-nowrap">
                            {description}
                        </TooltipContent>
                    </Tooltip>
                ))}
            </div>
            <FieldError message={fieldErrors.promptTemplate} id={err.id("promptTemplate")} />
        </PageInlineRow>
    );

    const scoresField = (
        <PageInlineRow
            label={
                <span className="inline-flex items-center gap-1.5">
                    Scores
                    <InfoRichTooltip ariaLabel="About scores" content={SCORES_TIPS_DESCRIPTION} />
                </span>
            }
            required
            action={
                !disabled && (
                    <Button variant="link" size="sm" className="h-auto p-0 text-xs" onClick={addChoiceScore} disabled={!canAddScore}>
                        <Plus className="mr-0.5 size-3" />
                        Add Score
                    </Button>
                )
            }
        >
            <div className="flex items-center gap-2 pl-6 text-xs text-(--secondary-text-wMain)">
                <span className="flex-1">
                    Criteria Assessment<span className="ml-0.5 text-(--brand-wMain)">*</span>
                </span>
                <span className="w-24">
                    Score<span className="ml-0.5 text-(--brand-wMain)">*</span>
                </span>
                <span className="size-9 shrink-0" aria-hidden />
            </div>
            <div className="flex flex-col gap-2">
                {value.choiceScores.map((cs, idx) => (
                    <div key={cs.id} className={cn("flex items-center gap-2 rounded-xs transition-colors", drag.isDropTarget(idx) && "bg-(--primary-w10)", drag.isDragging(idx) && "opacity-50")} {...drag.rowProps(idx)}>
                        <DragHandle label={cs.choice || "score"} disabled={disabled} handleProps={drag.handleProps(idx)} />
                        <Input className="flex-1" placeholder="Choice label" value={cs.choice} onChange={e => updateChoiceScore(idx, "choice", e.target.value)} disabled={disabled} />
                        <div className="relative w-24">
                            <Input
                                className="pr-6"
                                type="number"
                                min={0}
                                max={100}
                                step={1}
                                placeholder="0-100"
                                value={Math.round(cs.score * 100)}
                                onChange={e => {
                                    const pct = Number(e.target.value);
                                    updateChoiceScore(idx, "score", Number.isFinite(pct) ? pct / 100 : 0);
                                }}
                                disabled={disabled}
                            />
                            <span className="pointer-events-none absolute top-1/2 right-2 -translate-y-1/2 text-sm text-(--secondary-text-wMain)">%</span>
                        </div>
                        {!disabled && (
                            <Button variant="ghost" size="icon" className="shrink-0" onClick={() => removeChoiceScore(idx)} disabled={!canRemoveScore} aria-label={`Remove ${cs.choice || "score"}`}>
                                <X className="size-4 text-(--secondary-text-wMain)" />
                            </Button>
                        )}
                    </div>
                ))}
            </div>
            {!canRemoveScore && <span className="text-xs text-(--secondary-text-wMain)">A minimum of {MIN_SCORES} scores is required.</span>}
            {!canAddScore && <span className="text-xs text-(--secondary-text-wMain)">A maximum of {MAX_SCORES} scores is supported.</span>}
            <FieldError message={fieldErrors.choiceScores} id={err.id("choiceScores")} />
        </PageInlineRow>
    );

    const errorNode = error ? <span className="text-xs text-(--error-wMain)">{error}</span> : null;

    if (isDecision) {
        return (
            <div className={cn("flex flex-col gap-7", variant === "page" && "max-w-3xl")}>
                {topFields}
                <DecisionEvaluatorFields value={value.decision} onChange={decision => patch({ decision })} disabled={disabled} errors={fieldErrors} selfId={evaluatorId} />
                {errorNode}
            </div>
        );
    }

    if (variant === "dialog") {
        return (
            <RichTooltipProvider>
                <div className="flex flex-col gap-7">
                    {topFields}
                    {criteriaField}
                    {scoresField}
                    {errorNode}
                </div>
            </RichTooltipProvider>
        );
    }

    const fieldColumnClass = "min-w-0 max-w-2xl flex-1 basis-[400px]";
    const bannerColumnClass = "w-full max-w-96 sm:w-96 sm:shrink-0";

    return (
        <RichTooltipProvider>
            <div className="flex flex-col gap-10">
                <div className="max-w-2xl">{topFields}</div>

                <div className="flex flex-wrap items-start gap-6">
                    <div className={fieldColumnClass}>{criteriaField}</div>
                    <aside className={bannerColumnClass}>
                        <InfoTipCard storageKey={CRITERIA_BANNER_KEY} header={CRITERIA_TIPS_HEADER} description={CRITERIA_TIPS_DESCRIPTION} learnMoreText="Learn about writing evaluation criteria" learnMoreUrl={EVAL_DOCS.WRITING_CRITERIA} />
                    </aside>
                </div>

                <div className="flex flex-wrap items-start gap-6">
                    <div className={fieldColumnClass}>{scoresField}</div>
                    <aside className={bannerColumnClass}>
                        <InfoTipCard storageKey={SCORES_BANNER_KEY} header={SCORES_TIPS_HEADER} description={SCORES_TIPS_DESCRIPTION} learnMoreText="Learn about choice scores" learnMoreUrl={EVAL_DOCS.CHOICE_SCORES} />
                    </aside>
                </div>

                {errorNode}
            </div>
        </RichTooltipProvider>
    );
}
