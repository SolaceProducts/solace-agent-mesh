// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo, useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";

import { evaluationsApi } from "@/api/evaluations";

import { cn } from "@/utils/cnTailwind";

import { useAgentOptions } from "@/features/entrypoints/hooks";

import { EmptyState } from "@/common";

import { useEvalRunResultDetail } from "../hooks";

import { ArtifactsSection } from "./ArtifactsSection";
import { ScoreBadge } from "./ScoreBadge";

type DiffToken = { text: string; type: "same" | "removed" | "added" };

function wordDiff(a: string, b: string): DiffToken[] {
    const aTokens = a.split(/(\s+)/);
    const bTokens = b.split(/(\s+)/);
    const m = aTokens.length;
    const n = bTokens.length;

    const dp: number[][] = Array.from({ length: m + 1 }, () => new Array(n + 1).fill(0));
    for (let i = 1; i <= m; i++) {
        for (let j = 1; j <= n; j++) {
            dp[i][j] = aTokens[i - 1] === bTokens[j - 1] ? dp[i - 1][j - 1] + 1 : Math.max(dp[i - 1][j], dp[i][j - 1]);
        }
    }

    const ops: Array<"same" | "removed" | "added"> = [];
    const texts: string[] = [];
    let i = m,
        j = n;
    while (i > 0 || j > 0) {
        if (i > 0 && j > 0 && aTokens[i - 1] === bTokens[j - 1]) {
            ops.unshift("same");
            texts.unshift(aTokens[i - 1]);
            i--;
            j--;
        } else if (j > 0 && (i === 0 || dp[i][j - 1] >= dp[i - 1][j])) {
            ops.unshift("added");
            texts.unshift(bTokens[j - 1]);
            j--;
        } else {
            ops.unshift("removed");
            texts.unshift(aTokens[i - 1]);
            i--;
        }
    }

    return ops.map((type, idx) => ({ text: texts[idx], type }));
}

interface EvalResultInspectorProps {
    readonly resultId: string;
    readonly evaluatorNameMap?: Map<string, string>;
}

export function EvalResultInspector({ resultId, evaluatorNameMap }: EvalResultInspectorProps) {
    const { result, loading, error } = useEvalRunResultDetail(resultId);
    const agentOptions = useAgentOptions();
    const agentDisplayNameMap = useMemo(() => Object.fromEntries(agentOptions.map(a => [a.name, a.displayName])), [agentOptions]);
    const [rawExpanded, setRawExpanded] = useState(false);
    const [diffMode, setDiffMode] = useState(false);

    if (loading) return <EmptyState variant="loading" title="Loading result..." />;
    if (error || result === null) return <EmptyState variant="error" title="Failed to load result" />;

    const hasExpectedResponse = result.expectedResponse !== undefined;
    const hasGoldenAnswer = hasExpectedResponse;

    return (
        <div className="flex flex-col divide-y">
            <div className="flex items-start justify-between gap-3 px-4 py-3">
                <div className="text-sm leading-snug font-semibold">{result.exampleId}</div>
                <div className="flex shrink-0 items-center gap-1.5">
                    {result.scores.map(s => (
                        <span key={s.id} className="flex items-center gap-1">
                            <span className="text-xs text-(--secondary-text-wMain)">{s.evaluatorId}</span>
                            <ScoreBadge score={s.score} />
                        </span>
                    ))}
                </div>
            </div>

            <div className="flex flex-col gap-2 px-4 py-3">
                <div className="text-xs font-semibold tracking-wide text-(--secondary-text-wMain) uppercase">Input</div>
                <div className="rounded-xs bg-(--secondary-w5) px-3 py-2 text-sm">{result.inputPrompt ?? "—"}</div>
                {result.inputArtifact && <ArtifactsSection artifacts={[result.inputArtifact]} fetchBlob={a => evaluationsApi.getRunResultExampleArtifactBlob(result.id, "input", a.filename, a.version)} />}
                {result.targetAgent && (
                    <div className="flex flex-wrap items-center gap-1.5">
                        <span className="flex items-center gap-1 text-xs text-(--secondary-text-wMain)">
                            {result.targetType === "workflow" ? "Workflow:" : "Agent:"}
                            <span className="rounded-full bg-(--secondary-w10) px-2 py-0.5 font-mono text-xs text-(--primary-text-wMain)">{agentDisplayNameMap[result.targetAgent] ?? result.targetAgent}</span>
                        </span>
                    </div>
                )}
            </div>

            {result.expectedArtifact && (
                <div className="flex flex-col gap-2 px-4 py-3">
                    <div className="text-xs font-semibold tracking-wide text-(--secondary-text-wMain) uppercase">Expected File</div>
                    <ArtifactsSection artifacts={[result.expectedArtifact]} fetchBlob={a => evaluationsApi.getRunResultExampleArtifactBlob(result.id, "expected", a.filename, a.version)} />
                </div>
            )}

            {hasGoldenAnswer ? (
                <div className="flex flex-col">
                    <div className="flex items-center justify-between px-4 py-3">
                        <div className="text-xs font-semibold tracking-wide text-(--secondary-text-wMain) uppercase">Expected vs Actual</div>
                        {hasExpectedResponse && (
                            <button
                                className={cn(
                                    "flex items-center gap-1.5 rounded border px-2 py-0.5 text-xs transition-colors",
                                    diffMode ? "border-(--brand-w100) bg-(--brand-w100)/10 text-(--brand-w100)" : "border-(--secondary-w10) text-(--secondary-text-wMain) hover:text-(--primary-text-wMain)"
                                )}
                                onClick={() => setDiffMode(prev => !prev)}
                            >
                                Diff {diffMode ? "●" : "○"}
                            </button>
                        )}
                    </div>

                    {diffMode && hasExpectedResponse ? (
                        <div className="px-4 pb-3">
                            <div className="rounded-xs bg-(--secondary-w5) px-3 py-2 font-mono text-sm leading-relaxed whitespace-pre-wrap">
                                {wordDiff(result.expectedResponse!, result.agentResponse ?? "").map((token, idx) => (
                                    <span key={idx} className={cn(token.type === "removed" && "bg-(--error-wMain)/15 text-(--error-wMain) line-through", token.type === "added" && "bg-(--success-wMain)/15 text-(--success-wMain)")}>
                                        {token.text}
                                    </span>
                                ))}
                            </div>
                        </div>
                    ) : (
                        <div className="flex flex-col divide-y">
                            <div className="flex flex-col gap-2 px-4 pb-3">{hasExpectedResponse && <div className="text-sm whitespace-pre-wrap text-(--secondary-text-wMain)">{result.expectedResponse}</div>}</div>
                            <div className="flex flex-col gap-2 px-4 py-3">
                                <div className="text-xs font-medium text-(--secondary-text-wMain)">Actual:</div>
                                <div className="text-sm whitespace-pre-wrap text-(--primary-text-wMain)">{result.agentResponse ?? "—"}</div>
                            </div>
                        </div>
                    )}
                </div>
            ) : (
                <div className="flex flex-col gap-2 px-4 py-3">
                    <div className="text-xs font-semibold tracking-wide text-(--secondary-text-wMain) uppercase">Actual Output</div>
                    <div className="text-sm whitespace-pre-wrap text-(--primary-text-wMain)">{result.agentResponse ?? "—"}</div>
                </div>
            )}

            {result.scores.length > 0 && (
                <div className="flex flex-col divide-y">
                    {result.scores.map(s => (
                        <div key={s.id} className="flex flex-col gap-2 px-4 py-3">
                            <div className="flex items-center justify-between">
                                <div className="text-xs font-semibold tracking-wide text-(--secondary-text-wMain) uppercase">{evaluatorNameMap?.get(s.evaluatorId) ?? s.evaluatorId}</div>
                                <div className="flex items-center gap-2">
                                    {s.passed !== undefined && (
                                        <span className={cn("rounded-full px-2 py-0.5 text-xs font-medium", s.passed ? "bg-(--success-wMain)/15 text-(--success-wMain)" : "bg-(--error-wMain)/15 text-(--error-wMain)")}>
                                            {s.passed ? "Passed" : "Failed"}
                                        </span>
                                    )}
                                    <ScoreBadge score={s.score} />
                                </div>
                            </div>
                            {s.reasoning && <blockquote className="border-l-2 border-(--brand-w100) pl-3 text-sm text-(--secondary-text-wMain) italic">{s.reasoning}</blockquote>}
                        </div>
                    ))}
                </div>
            )}

            {result.agentResponse && (
                <div className="px-4 py-3">
                    <button className="flex items-center gap-1 text-xs font-semibold tracking-wide text-(--secondary-text-wMain) uppercase transition-colors hover:text-(--primary-text-wMain)" onClick={() => setRawExpanded(prev => !prev)}>
                        {rawExpanded ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
                        Raw Response
                    </button>
                    {rawExpanded && <div className="mt-2 rounded-xs bg-(--secondary-w5) p-3 font-mono text-xs whitespace-pre-wrap">{result.agentResponse}</div>}
                </div>
            )}
        </div>
    );
}
