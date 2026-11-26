// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useId } from "react";
import { Plus, X } from "lucide-react";

import { useEvalEvaluators } from "@/api/evaluations";

import { type RadioOption, RadioOptions } from "@/common/form";
import { PageInlineRow } from "@/common/layout";

import { cn } from "@/utils/cnTailwind";
import { decisionQuestionTypeLabel } from "@/utils/decisionNode";

import { Button } from "@/ui/button";
import { Checkbox } from "@/ui/checkbox";
import { Input } from "@/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/ui/select";
import { Textarea } from "@/ui/textarea";
import type { EvaluatorQuestionType, EvaluatorRead, EvaluatorScoring } from "@/types/eval";

import { useDragReorder } from "../hooks/useDragReorder";

import { DecisionModelSelect } from "./DecisionModelSelect";
import { DragHandle } from "./DragHandle";
import {
    ALL_DECISION_READS,
    DECISION_READ_LABELS,
    DECISION_SCORING_LABELS,
    type DecisionFormValue,
    type DecisionLevel,
    type DecisionOption,
    errorIds,
    type EvaluatorFieldErrors,
    MAX_CHOICES,
    MAX_LEVELS,
    MIN_CHOICES,
    MIN_LEVELS,
    newDecisionLevel,
    newDecisionOption,
} from "./evaluatorForm";

const NO_ESCALATION = "__none__";

const QUESTION_TYPE_OPTIONS: readonly { value: EvaluatorQuestionType; label: string; description: string }[] = (
    [
        ["choice", "Picks one option; each option has its own score."],
        ["score", "Rates on ordered levels; the lowest scores 0% and the highest 100%."],
        ["boolean", "Answers yes or no; the score is the probability that the answer is true."],
    ] as const
).map(([value, description]) => ({ value, label: decisionQuestionTypeLabel(value), description }));

const SCORING_OPTIONS: RadioOption[] = [
    { value: "chosen", label: DECISION_SCORING_LABELS.chosen, description: "Score the option the model picks, as an LLM judge does." },
    { value: "expected", label: DECISION_SCORING_LABELS.expected, description: "Weight every option's score by its probability, for a smoother score." },
];

const READ_LABELS = DECISION_READ_LABELS;

export function FieldError({ message, id }: { readonly message?: string; readonly id?: string }) {
    if (!message) return null;
    return (
        <span id={id} role="alert" className="text-xs text-(--error-wMain)">
            {message}
        </span>
    );
}

function PercentInput({ value, onChange, disabled, ariaLabel }: { readonly value: number; readonly onChange: (v: number) => void; readonly disabled?: boolean; readonly ariaLabel: string }) {
    return (
        <div className="relative w-24 shrink-0">
            <Input
                className="pr-6"
                type="number"
                min={0}
                max={100}
                step="any"
                placeholder="0-100"
                aria-label={ariaLabel}
                value={Number((value * 100).toFixed(4))}
                onChange={e => {
                    const pct = Number(e.target.value);
                    onChange(Number.isFinite(pct) ? Number((pct / 100).toFixed(6)) : 0);
                }}
                disabled={disabled}
            />
            <span className="pointer-events-none absolute top-1/2 right-2 -translate-y-1/2 text-sm text-(--secondary-text-wMain)">%</span>
        </div>
    );
}

interface OptionsEditorProps {
    readonly options: DecisionOption[];
    readonly onChange: (options: DecisionOption[]) => void;
    readonly disabled?: boolean;
    readonly error?: string;
    readonly errorId: string;
}

function OptionsEditor({ options, onChange, disabled, error, errorId }: OptionsEditorProps) {
    const drag = useDragReorder(options, onChange, disabled);
    const update = (index: number, p: Partial<DecisionOption>) => onChange(options.map((o, i) => (i === index ? { ...o, ...p } : o)));
    const canRemove = options.length > MIN_CHOICES;
    const canAdd = options.length < MAX_CHOICES;
    return (
        <PageInlineRow
            label="Options"
            required
            action={
                !disabled && (
                    <Button variant="link" size="sm" className="h-auto p-0 text-xs" onClick={() => onChange([...options, newDecisionOption()])} disabled={!canAdd}>
                        <Plus className="mr-0.5 size-3" />
                        Add Option
                    </Button>
                )
            }
        >
            <div className="flex items-center gap-2 pl-6 text-xs text-(--secondary-text-wMain)">
                <span className="w-40 shrink-0">
                    Option<span className="ml-0.5 text-(--brand-wMain)">*</span>
                </span>
                <span className="flex-1">Description</span>
                <span className="w-24">
                    Score<span className="ml-0.5 text-(--brand-wMain)">*</span>
                </span>
                {!disabled && <span className="size-9 shrink-0" aria-hidden />}
            </div>
            <fieldset className="flex min-w-0 flex-col gap-2" aria-label="Options" aria-describedby={error ? errorId : undefined}>
                {options.map((o, idx) => (
                    <div key={o.id} className={cn("flex items-center gap-2 rounded-xs transition-colors", drag.isDropTarget(idx) && "bg-(--primary-w10)", drag.isDragging(idx) && "opacity-50")} {...drag.rowProps(idx)}>
                        <DragHandle label={o.key || "option"} disabled={disabled} handleProps={drag.handleProps(idx)} />
                        <Input className="w-40 shrink-0" placeholder="e.g. correct" aria-label={`Option ${idx + 1}`} value={o.key} onChange={e => update(idx, { key: e.target.value })} disabled={disabled} />
                        <Input className="flex-1" placeholder="What this option means" aria-label={`Option ${idx + 1} description`} value={o.description} onChange={e => update(idx, { description: e.target.value })} disabled={disabled} />
                        <PercentInput value={o.score} onChange={score => update(idx, { score })} disabled={disabled} ariaLabel={`Option ${idx + 1} score`} />
                        {!disabled && (
                            <Button variant="ghost" size="icon" className="shrink-0" onClick={() => onChange(options.filter((_, i) => i !== idx))} disabled={!canRemove} aria-label={`Remove ${o.key || "option"}`}>
                                <X className="size-4 text-(--secondary-text-wMain)" />
                            </Button>
                        )}
                    </div>
                ))}
            </fieldset>
            <FieldError message={error} id={errorId} />
        </PageInlineRow>
    );
}

interface LevelsEditorProps {
    readonly levels: DecisionLevel[];
    readonly onChange: (levels: DecisionLevel[]) => void;
    readonly disabled?: boolean;
    readonly error?: string;
    readonly errorId: string;
}

function LevelsEditor({ levels, onChange, disabled, error, errorId }: LevelsEditorProps) {
    const drag = useDragReorder(levels, onChange, disabled);
    const update = (index: number, p: Partial<DecisionLevel>) => onChange(levels.map((l, i) => (i === index ? { ...l, ...p } : l)));
    const canRemove = levels.length > MIN_LEVELS;
    const canAdd = levels.length < MAX_LEVELS;
    return (
        <PageInlineRow
            label="Levels"
            required
            action={
                !disabled && (
                    <Button variant="link" size="sm" className="h-auto p-0 text-xs" onClick={() => onChange([...levels, newDecisionLevel()])} disabled={!canAdd}>
                        <Plus className="mr-0.5 size-3" />
                        Add Level
                    </Button>
                )
            }
        >
            <span className="text-xs text-(--secondary-text-wMain)">Lowest first. The lowest level scores 0%, the highest 100%, and the levels between are evenly spaced.</span>
            <fieldset className="flex min-w-0 flex-col gap-2" aria-label="Levels" aria-describedby={error ? errorId : undefined}>
                {levels.map((l, idx) => (
                    <div key={l.id} className={cn("flex items-center gap-2 rounded-xs transition-colors", drag.isDropTarget(idx) && "bg-(--primary-w10)", drag.isDragging(idx) && "opacity-50")} {...drag.rowProps(idx)}>
                        <DragHandle label={l.key || "level"} disabled={disabled} handleProps={drag.handleProps(idx)} />
                        <Input className="w-40 shrink-0" placeholder="e.g. good" aria-label={`Level ${idx + 1}`} value={l.key} onChange={e => update(idx, { key: e.target.value })} disabled={disabled} />
                        <Input className="flex-1" placeholder="What this level means" aria-label={`Level ${idx + 1} description`} value={l.description} onChange={e => update(idx, { description: e.target.value })} disabled={disabled} />
                        {!disabled && (
                            <Button variant="ghost" size="icon" className="shrink-0" onClick={() => onChange(levels.filter((_, i) => i !== idx))} disabled={!canRemove} aria-label={`Remove ${l.key || "level"}`}>
                                <X className="size-4 text-(--secondary-text-wMain)" />
                            </Button>
                        )}
                    </div>
                ))}
            </fieldset>
            <FieldError message={error} id={errorId} />
        </PageInlineRow>
    );
}

interface DecisionEvaluatorFieldsProps {
    readonly value: DecisionFormValue;
    readonly onChange: (v: DecisionFormValue) => void;
    readonly disabled?: boolean;
    readonly errors?: EvaluatorFieldErrors;
    readonly selfId?: string;
}

export function DecisionEvaluatorFields({ value, onChange, disabled, errors = {}, selfId }: DecisionEvaluatorFieldsProps) {
    const patch = (p: Partial<DecisionFormValue>) => onChange({ ...value, ...p });
    const { data: evaluators = [] } = useEvalEvaluators();
    const judges = evaluators.filter(e => e.type === "llm_judge" && e.id !== selfId);
    const err = errorIds(useId(), errors);

    const toggleRead = (read: EvaluatorRead, on: boolean) => {
        const next = on ? [...value.reads, read] : value.reads.filter(r => r !== read);
        patch({ reads: ALL_DECISION_READS.filter(r => next.includes(r)) });
    };

    const escalates = value.escalateToEvaluatorId !== "";

    return (
        <div className="flex flex-col gap-7" data-testid="decisionEvaluatorFields">
            <PageInlineRow label="Decision Model" required>
                <DecisionModelSelect value={value.model} onChange={model => patch({ model })} disabled={disabled} invalid={!!errors.decisionModel} describedBy={err.describedBy("decisionModel")} />
                <FieldError message={errors.decisionModel} id={err.id("decisionModel")} />
            </PageInlineRow>

            <PageInlineRow label="Question Type" required>
                <Select value={value.questionType} onValueChange={t => patch({ questionType: t as EvaluatorQuestionType })} disabled={disabled}>
                    <SelectTrigger className="w-full" aria-label="Question Type" invalid={!!errors.question} aria-describedby={err.describedBy("question")} data-testid="questionTypeSelect">
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        {QUESTION_TYPE_OPTIONS.map(o => (
                            <SelectItem key={o.value} value={o.value} description={o.description}>
                                {o.label}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
                <FieldError message={errors.question} id={err.id("question")} />
            </PageInlineRow>

            <PageInlineRow label="Question" required>
                <Textarea
                    value={value.instructions}
                    onChange={e => patch({ instructions: e.target.value })}
                    placeholder="Does the response answer the prompt correctly and completely?"
                    aria-label="Question"
                    rows={3}
                    disabled={disabled}
                    aria-invalid={!!errors.instructions}
                    aria-describedby={err.describedBy("instructions")}
                />
                <span className="text-xs text-(--secondary-text-wMain)">Write the question for the decision model. It reads the case fields you choose below; no template variables are needed.</span>
                <FieldError message={errors.instructions} id={err.id("instructions")} />
            </PageInlineRow>

            {value.questionType === "choice" && <OptionsEditor options={value.options} onChange={options => patch({ options })} disabled={disabled} error={errors.options} errorId={err.id("options")} />}
            {value.questionType === "score" && <LevelsEditor levels={value.levels} onChange={levels => patch({ levels })} disabled={disabled} error={errors.levels} errorId={err.id("levels")} />}
            {value.questionType === "boolean" && (
                <PageInlineRow label="Answers">
                    <Input
                        placeholder="What true means (optional)"
                        aria-label="True description"
                        aria-describedby={err.describedBy("booleanCriteria")}
                        value={value.trueDescription}
                        onChange={e => patch({ trueDescription: e.target.value })}
                        disabled={disabled}
                    />
                    <Input
                        placeholder="What false means (optional)"
                        aria-label="False description"
                        aria-describedby={err.describedBy("booleanCriteria")}
                        value={value.falseDescription}
                        onChange={e => patch({ falseDescription: e.target.value })}
                        disabled={disabled}
                    />
                    <span className="text-xs text-(--secondary-text-wMain)">Describe both answers, or neither.</span>
                    <FieldError message={errors.booleanCriteria} id={err.id("booleanCriteria")} />
                </PageInlineRow>
            )}

            {value.questionType !== "boolean" && (
                <PageInlineRow label="Scoring">
                    <RadioOptions ariaLabel="Scoring" orientation="vertical" options={SCORING_OPTIONS} value={value.scoring} onChange={s => patch({ scoring: s as EvaluatorScoring })} disabled={disabled} />
                    <FieldError message={errors.scoring} id={err.id("scoring")} />
                </PageInlineRow>
            )}

            <PageInlineRow label="Reads" required>
                <fieldset className="flex min-w-0 flex-col gap-2" aria-label="Reads" aria-describedby={err.describedBy("reads")}>
                    {ALL_DECISION_READS.map(read => (
                        <label key={read} className={cn("flex items-baseline gap-2 text-sm", read === "response" || disabled ? "cursor-not-allowed" : "cursor-pointer")}>
                            <Checkbox className="self-center" checked={value.reads.includes(read)} onCheckedChange={on => toggleRead(read, on)} disabled={disabled || read === "response"} aria-label={READ_LABELS[read]} />
                            {READ_LABELS[read]}
                        </label>
                    ))}
                </fieldset>
                <span className="text-xs text-(--secondary-text-wMain)">What the decision model is sent for each case. It always reads the response. Text files are sent with their content, and other files by name and type only.</span>
                <FieldError message={errors.reads} id={err.id("reads")} />
            </PageInlineRow>

            <PageInlineRow label="Escalate To">
                <Select value={value.escalateToEvaluatorId || NO_ESCALATION} onValueChange={id => patch(id === NO_ESCALATION ? { escalateToEvaluatorId: "", minConfidencePercent: "" } : { escalateToEvaluatorId: id })} disabled={disabled}>
                    <SelectTrigger className="w-full" aria-label="Escalate to" invalid={!!errors.escalateToEvaluatorId} aria-describedby={err.describedBy("escalateToEvaluatorId")} data-testid="escalateToSelect">
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value={NO_ESCALATION}>No escalation</SelectItem>
                        {escalates && !judges.some(j => j.id === value.escalateToEvaluatorId) && (
                            <SelectItem value={value.escalateToEvaluatorId} disabled>
                                {value.escalateToEvaluatorId} (not found)
                            </SelectItem>
                        )}
                        {judges.map(j => (
                            <SelectItem key={j.id} value={j.id}>
                                {j.name}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
                <span className="text-xs text-(--secondary-text-wMain)">
                    An LLM-judge evaluator that scores the cases the decision model cannot: below the confidence threshold if you set one, and when the provider fails. A rejected or denied API key, or a configuration error, is not escalated.
                </span>
                <FieldError message={errors.escalateToEvaluatorId} id={err.id("escalateToEvaluatorId")} />
            </PageInlineRow>

            {escalates && (
                <PageInlineRow label="Minimum Confidence">
                    <div className="relative w-28">
                        <Input
                            className="pr-6"
                            type="number"
                            min={0}
                            max={100}
                            step="any"
                            placeholder="e.g. 80"
                            aria-label="Minimum confidence"
                            value={value.minConfidencePercent}
                            onChange={e => patch({ minConfidencePercent: e.target.value })}
                            disabled={disabled}
                            aria-invalid={!!errors.minConfidence}
                            aria-describedby={err.describedBy("minConfidence")}
                        />
                        <span className="pointer-events-none absolute top-1/2 right-2 -translate-y-1/2 text-sm text-(--secondary-text-wMain)">%</span>
                    </div>
                    <span className="text-xs text-(--secondary-text-wMain)">
                        Escalate answers below this confidence. Leave it blank to escalate only when the provider fails. A rejected or denied API key, or a configuration error, is never escalated.
                    </span>
                    <FieldError message={errors.minConfidence} id={err.id("minConfidence")} />
                </PageInlineRow>
            )}
        </div>
    );
}
