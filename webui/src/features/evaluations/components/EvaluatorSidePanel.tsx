// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { X } from "lucide-react";
import { useNavigate } from "react-router-dom";

import { useEvalEvaluators } from "@/api/evaluations";
import { useModelConfigs } from "@/api/models/hooks";
import type { ModelConfig } from "@/api/models/types";

import { Button } from "@/ui/button";
import type { EvalEvaluator } from "@/types/eval";

import { ModelConfigIcon } from "@/features/activation";

import { EVAL_ROUTES } from "../constants";

import { ChoiceScoresList } from "./ChoiceScoresList";
import { DecisionEvaluatorSummary } from "./DecisionEvaluatorSummary";
import { decisionSettingsFromEvaluator, DEFAULT_JUDGE_MODEL_LABEL, findModelByRef } from "./evaluatorForm";
import { HighlightedPromptText } from "./HighlightedPromptText";

interface EvaluatorSidePanelProps {
    readonly evaluator: EvalEvaluator;
    readonly onClose: () => void;
}

function JudgeModel({ evaluator, resolvedModel }: { readonly evaluator: EvalEvaluator; readonly resolvedModel: ModelConfig | undefined }) {
    if (resolvedModel) {
        return (
            <div className="flex items-center gap-2">
                <ModelConfigIcon model={resolvedModel} size="xs" />
                <span className="text-sm font-medium">{resolvedModel.alias}</span>
                {resolvedModel.modelName && <span className="font-mono text-xs text-(--secondary-text-wMain)">{resolvedModel.modelName}</span>}
            </div>
        );
    }
    if (evaluator.model) {
        return <span className="text-sm font-medium">{evaluator.model}</span>;
    }
    return <span className="text-sm text-(--secondary-text-wMain)">{evaluator.isDefault ? DEFAULT_JUDGE_MODEL_LABEL : "No model configured"}</span>;
}

export function EvaluatorSidePanel({ evaluator, onClose }: EvaluatorSidePanelProps) {
    const navigate = useNavigate();
    const { data: modelConfigs = [] } = useModelConfigs();
    const resolvedModel = findModelByRef(modelConfigs, evaluator.model ?? undefined);
    const isLlmJudge = evaluator.type === "llm_judge";
    const isDecision = evaluator.type === "decision";
    const { data: evaluators = [] } = useEvalEvaluators();
    const escalateToName = evaluators.find(e => e.id === evaluator.escalateToEvaluatorId)?.name;

    return (
        <div className="flex h-full w-full flex-col border-l bg-(--background-w10)">
            <div className="flex h-16 items-center justify-between gap-2 border-b px-4">
                <div className="truncate text-xl font-semibold" title={evaluator.name}>
                    {evaluator.name}
                </div>
                <Button variant="ghost" tooltip="Close" onClick={onClose}>
                    <X />
                </Button>
            </div>

            <div className="flex-1 overflow-y-auto p-4">
                <div className="mb-6 flex flex-col gap-3 rounded-sm bg-(--secondary-w10) p-4">
                    <div className="text-sm font-semibold">Description</div>
                    <div className="text-sm">{evaluator.description || <span className="text-(--secondary-text-wMain)">No description</span>}</div>
                    <div className="mt-1 flex flex-col gap-2">
                        {(!evaluator.isDefault || isLlmJudge) && (
                            <Button variant="outline" onClick={() => navigate(EVAL_ROUTES.evaluator(evaluator.id))}>
                                Open Evaluator
                            </Button>
                        )}
                        <Button onClick={() => navigate(EVAL_ROUTES.experimentForEvaluator(evaluator.id))}>Create Experiment</Button>
                    </div>
                </div>

                {isDecision && (
                    <DecisionEvaluatorSummary
                        settings={decisionSettingsFromEvaluator(evaluator, escalateToName)}
                        model={
                            resolvedModel ? (
                                <span className="flex items-center gap-2">
                                    <ModelConfigIcon model={resolvedModel} size="xs" />
                                    <span className="font-medium">{resolvedModel.alias}</span>
                                    {resolvedModel.modelName && <span className="font-mono text-xs text-(--secondary-text-wMain)">{resolvedModel.modelName}</span>}
                                </span>
                            ) : (
                                (evaluator.model ?? <span className="text-(--secondary-text-wMain)">No model configured</span>)
                            )
                        }
                    />
                )}

                {isLlmJudge && (
                    <>
                        <div className="mb-2 text-sm font-semibold">Criteria</div>
                        <div className="mb-6">
                            {evaluator.promptTemplate ? (
                                <div className="rounded-sm bg-(--secondary-w10) p-4 text-sm whitespace-pre-wrap">
                                    <HighlightedPromptText text={evaluator.promptTemplate} />
                                </div>
                            ) : (
                                <div className="rounded-sm bg-(--secondary-w10) py-2 text-center text-sm text-(--secondary-text-wMain)">No criteria defined.</div>
                            )}
                        </div>

                        <div className="mb-2 text-sm font-semibold">Scores</div>
                        <div className="mb-6">
                            {evaluator.choiceScores && Object.keys(evaluator.choiceScores).length > 0 ? (
                                <ChoiceScoresList scores={evaluator.choiceScores} />
                            ) : (
                                <div className="rounded-sm bg-(--secondary-w10) py-2 text-center text-sm text-(--secondary-text-wMain)">No scores defined.</div>
                            )}
                        </div>

                        <div className="mb-2 text-sm font-semibold">Model</div>
                        <div className="rounded-sm bg-(--secondary-w10) px-4 py-3">
                            <JudgeModel evaluator={evaluator} resolvedModel={resolvedModel} />
                        </div>
                    </>
                )}
            </div>
        </div>
    );
}
