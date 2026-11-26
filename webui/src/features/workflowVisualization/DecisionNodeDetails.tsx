// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { CircleHelp, Route } from "lucide-react";
import type { FC, ReactNode } from "react";

import type { WorkflowNodeConfig } from "@/utils/agentUtils";
import { confidenceThresholdLabel, decisionQuestions, decisionQuestionTypeLabel, escalationModeOf, routedQuestionFirst } from "@/utils/decisionNode";

import { Badge, Tooltip, TooltipContent, TooltipTrigger } from "@/ui";

interface DecisionNodeDetailsProps {
    config: WorkflowNodeConfig;
}

const DetailField: FC<{ label: string; hint?: string; children: ReactNode }> = ({ label, hint, children }) => (
    <div className="mb-4">
        <span className="mb-1 flex items-center gap-1.5 text-sm font-medium text-(--secondary-text-wMain)">
            {label}
            {hint && (
                <Tooltip>
                    <TooltipTrigger asChild>
                        <button type="button" className="text-(--secondary-text-wMain) hover:text-(--primary-text-wMain)" aria-label={`${label} help`}>
                            <CircleHelp className="size-3.5" />
                        </button>
                    </TooltipTrigger>
                    <TooltipContent side="right" className="max-w-64">
                        {hint}
                    </TooltipContent>
                </Tooltip>
            )}
        </span>
        {children}
    </div>
);

const QuestionFieldLabel: FC<{ children: ReactNode }> = ({ children }) => <span className="mb-1 block text-xs font-medium text-(--secondary-text-wMain)">{children}</span>;

const DecisionNodeDetails: FC<DecisionNodeDetailsProps> = ({ config }) => {
    const questions = routedQuestionFirst(decisionQuestions(config), config.route_on);
    const inputFields = Object.entries(config.input ?? {});

    return (
        <div data-testid="decisionNodeDetails">
            {inputFields.length > 0 && (
                <DetailField label="Input" hint="The decision model answers the questions below using this input.">
                    <div className="space-y-1 rounded bg-(--secondary-w10) p-2 font-mono text-xs">
                        {inputFields.map(([field, value]) => (
                            <div key={field} className="break-all">
                                <span className="font-semibold">{field}</span>: {typeof value === "string" ? value : JSON.stringify(value)}
                            </div>
                        ))}
                    </div>
                </DetailField>
            )}

            {questions.length > 0 && (
                <DetailField label="Questions">
                    <div className="space-y-2">
                        {questions.map(question => (
                            <div key={question.key} className="rounded border border-(--secondary-w20) p-2">
                                <div className="flex items-center gap-2">
                                    {question.key === config.route_on && (
                                        <Tooltip>
                                            <TooltipTrigger asChild>
                                                <button type="button" className="flex-shrink-0 text-(--secondary-text-wMain) hover:text-(--primary-text-wMain)" aria-label="Routes on this question">
                                                    <Route className="size-4" />
                                                </button>
                                            </TooltipTrigger>
                                            <TooltipContent side="right" className="max-w-64">
                                                The decision node routes on the answer to this question.
                                            </TooltipContent>
                                        </Tooltip>
                                    )}
                                    <code className="font-mono text-sm font-semibold">{question.key}</code>
                                    <Badge variant="outline">{decisionQuestionTypeLabel(question.type)}</Badge>
                                </div>
                                {question.instructions && (
                                    <div className="mt-2">
                                        <QuestionFieldLabel>Instructions</QuestionFieldLabel>
                                        <div className="text-sm">{question.instructions}</div>
                                    </div>
                                )}
                                {question.options.length > 0 && (
                                    <div className="mt-2">
                                        <QuestionFieldLabel>Choices</QuestionFieldLabel>
                                        <div className="flex flex-wrap gap-1" aria-label={`${question.key} options`}>
                                            {question.options.map(option => (
                                                <Badge key={option} variant="secondary" className="bg-(--secondary-w10) font-mono">
                                                    {option}
                                                </Badge>
                                            ))}
                                        </div>
                                    </div>
                                )}
                            </div>
                        ))}
                    </div>
                </DetailField>
            )}

            {!!config.min_confidence && (
                <DetailField label="Escalation Threshold">
                    <div className="text-sm">&lt; {confidenceThresholdLabel(config.min_confidence)}</div>
                </DetailField>
            )}

            {config.escalate && (
                <DetailField label="Escalation Mode" hint="reroute: the escalate target answers the routed-on question and its answer picks the route. handoff: the escalate target's reply ends the routing.">
                    <code className="font-mono text-sm">{escalationModeOf(config) ?? "reroute"}</code>
                </DetailField>
            )}

            {config.on_provider_error && (
                <DetailField label="On Provider Error" hint="fail (the default) fails the node. escalate sends a transient provider failure to the escalate target.">
                    <code className="font-mono text-sm">{config.on_provider_error}</code>
                </DetailField>
            )}
        </div>
    );
};

export default DecisionNodeDetails;
