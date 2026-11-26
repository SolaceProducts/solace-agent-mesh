// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type CSSProperties, type FC, type KeyboardEvent, type MouseEvent, type ReactNode, useState } from "react";
import { CircleCheckBig, ExternalLink, FileText, GitCommit, GitMerge, HardDrive, Link, List, MessageSquare, Share2, Signpost, Split, Terminal, User, Workflow, XCircle, Zap } from "lucide-react";

import { useFilteredChatArtifacts } from "@/api/artifacts";

import { useChatContext } from "@/providers/contexts";

import { MarkdownHTMLConverter } from "@/common/markdown";

import { decisionAnswerSummary, decisionEscalationLabel, type DecisionResult, decisionResultOf, decisionRoute } from "@/features/activities/decisionResult";
import { NODE_COLORS } from "@/features/activities/nodeColors";
import { ImageSearchGrid } from "@/features/chat/research";

import { JSONViewer } from "@/common";
import type {
    ArtifactInfo,
    ArtifactNotificationData,
    LLMCallData,
    LLMResponseToAgentData,
    ToolDecisionData,
    ToolInvocationStartData,
    ToolResultData,
    VisualizerStep,
    WorkflowExecutionResultData,
    WorkflowExecutionStartData,
    WorkflowNodeExecutionResultData,
    WorkflowNodeExecutionStartData,
} from "@/types";
import { isString } from "@/utils";

interface VisualizerStepCardProps {
    step: VisualizerStep;
    isHighlighted?: boolean;
    onClick?: () => void;
    variant?: "list" | "popover";
    artifactLookup?: (filename: string) => ArtifactInfo | undefined;
    onViewArtifact?: (artifact: ArtifactInfo, version?: number) => void;
}

interface LLMResponseToAgentDetailsProps {
    data: LLMResponseToAgentData;
}

const parseWebSearchResult = (resultData: unknown) => {
    let parsedResult = null;
    let hasImages = false;

    try {
        if (isString(resultData)) {
            parsedResult = JSON.parse(resultData);
        } else if (typeof resultData === "object") {
            parsedResult = resultData;
        }

        if (parsedResult?.result) {
            const innerResult = isString(parsedResult.result) ? JSON.parse(parsedResult.result) : parsedResult.result;
            if (innerResult?.images && Array.isArray(innerResult.images) && innerResult.images.length > 0) {
                hasImages = true;
            }
        }
    } catch {}

    return { parsedResult, hasImages };
};

const LLMResponseToAgentDetails: FC<LLMResponseToAgentDetailsProps> = ({ data }) => {
    const [expanded, setExpanded] = useState(false);

    const toggleExpand = (e: MouseEvent) => {
        e.stopPropagation();
        setExpanded(!expanded);
    };

    if (!expanded) {
        return (
            <div className="mt-1.5 flex items-center justify-between text-xs text-(--secondary-text-wMain)">
                <span className="italic">Internal LLM response</span>
                <button onClick={toggleExpand} className="text-xs text-(--info-wMain) underline hover:text-(--info-w100)">
                    Show details
                </button>
            </div>
        );
    }

    return (
        <div className="mt-1.5 rounded-md bg-(--background-w10) p-2 text-xs text-(--primary-text-wMain)">
            <div className="mb-1 flex items-center justify-between">
                <strong>LLM Response Details:</strong>
                <button onClick={toggleExpand} className="text-xs text-(--info-wMain) underline hover:text-(--info-w100)">
                    Hide details
                </button>
            </div>
            {data.modelName && (
                <p>
                    <strong>Model:</strong> {data.modelName}
                </p>
            )}
            <div className="mt-1">
                <p>
                    <strong>Response Preview:</strong>
                </p>
                <pre className="max-h-28 overflow-y-auto rounded bg-(--secondary-w10) p-1.5 font-mono text-xs break-all whitespace-pre-wrap">{data.responsePreview}</pre>
            </div>
            {data.isFinalResponse !== undefined && (
                <p className="mt-1">
                    <strong>Final Response:</strong> {data.isFinalResponse ? "Yes" : "No"}
                </p>
            )}
        </div>
    );
};

const getWorkflowNodeStartIcon = (nodeType: string | undefined): ReactNode => {
    switch (nodeType) {
        case "map":
            return <List className={`mr-2 ${NODE_COLORS.map}`} size={18} />;
        case "fork":
            return <Split className={`mr-2 ${NODE_COLORS.map}`} size={18} />;
        case "switch":
            return <GitMerge className={`mr-2 ${NODE_COLORS.switch}`} size={18} />;
        case "decision":
            return <Signpost className={`mr-2 ${NODE_COLORS.decision}`} size={18} />;
        default:
            return <GitCommit className={`mr-2 ${NODE_COLORS.map}`} size={18} />;
    }
};

const getStepIcon = (step: VisualizerStep): ReactNode => {
    switch (step.type) {
        case "USER_REQUEST":
            return <User className={`mr-2 ${NODE_COLORS.user}`} size={18} />;
        case "AGENT_RESPONSE_TEXT":
            return <Zap className={`mr-2 ${NODE_COLORS.llm}`} size={18} />;
        case "TASK_COMPLETED":
            return <CircleCheckBig className="mr-2 text-(--success-wMain)" size={18} />;
        case "TASK_FAILED":
            return <XCircle className="mr-2 text-(--error-wMain)" size={18} />;
        case "AGENT_LLM_CALL":
            return <Zap className={`mr-2 ${NODE_COLORS.group}`} size={18} />;
        case "AGENT_LLM_RESPONSE_TO_AGENT":
            return <Zap className={`mr-2 ${NODE_COLORS.llm}`} size={18} />;
        case "AGENT_LLM_RESPONSE_TOOL_DECISION": {
            const isPeer = step.data.toolDecision?.decisions?.[0]?.isPeerDelegation;
            return isPeer ? <Share2 className={`mr-2 ${NODE_COLORS.peer}`} size={18} /> : <Terminal className={`mr-2 ${NODE_COLORS.peer}`} size={18} />;
        }
        case "AGENT_TOOL_INVOCATION_START":
            return step.data.toolInvocationStart?.isPeerInvocation ? <Share2 className={`mr-2 ${NODE_COLORS.tool}`} size={18} /> : <Terminal className={`mr-2 ${NODE_COLORS.tool}`} size={18} />;
        case "AGENT_TOOL_EXECUTION_RESULT":
            return <HardDrive className={`mr-2 ${NODE_COLORS.llm}`} size={18} />;
        case "AGENT_ARTIFACT_NOTIFICATION":
            return <FileText className={`mr-2 ${NODE_COLORS.artifact}`} size={18} />;
        case "WORKFLOW_EXECUTION_START":
        case "WORKFLOW_EXECUTION_RESULT":
            return <Workflow className={`mr-2 ${NODE_COLORS.group}`} size={18} />;
        case "WORKFLOW_NODE_EXECUTION_START":
            return getWorkflowNodeStartIcon(step.data.workflowNodeExecutionStart?.nodeType);
        case "WORKFLOW_NODE_EXECUTION_RESULT":
            return <GitCommit className="mr-2 text-(--success-wMain)" size={18} />;
        case "WORKFLOW_MAP_PROGRESS":
            return <List className={`mr-2 ${NODE_COLORS.map}`} size={18} />;
        default:
            return <MessageSquare className="mr-2 text-(--secondary-text-wMain)" size={18} />;
    }
};

const getDelegationText = (stepType: VisualizerStep["type"]): string => {
    switch (stepType) {
        case "AGENT_LLM_RESPONSE_TOOL_DECISION":
        case "AGENT_TOOL_INVOCATION_START":
            return "Delegated to: ";
        case "AGENT_TOOL_EXECUTION_RESULT":
            return "Response from: ";
        default:
            return "Peer Interaction with: ";
    }
};

const getIndentationStyle = (variant: "list" | "popover", nestingLevel: number | undefined): CSSProperties => (variant === "list" && nestingLevel && nestingLevel > 0 ? { marginLeft: `${nestingLevel * 24}px` } : {});

const getCardClasses = (variant: "list" | "popover", isHighlighted: boolean | undefined, hasOnClick: boolean): string => {
    const cursorClass = hasOnClick ? "cursor-pointer" : "";

    if (variant === "popover") {
        return `p-3 bg-transparent hover:bg-(--background-w10) transition-colors duration-150 ${cursorClass}`;
    }

    const borderClass = isHighlighted ? "border-(--info-wMain) ring-1 ring-(--info-wMain)" : "border-(--secondary-w20)";
    return `mb-3 p-3 border rounded-lg shadow-sm bg-(--background-w10) hover:shadow-md transition-shadow duration-150 ${borderClass} ${cursorClass}`;
};

const handleActivationKeyDown = (onClick: () => void) => (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        onClick();
    }
};

const renderLLMCallData = (data: LLMCallData): ReactNode => (
    <div className="mt-1.5 rounded-md bg-(--background-w10) p-2 text-xs text-(--primary-text-wMain)">
        <p>
            <strong>Model:</strong> {data.modelName}
        </p>
        <p className="mt-1">
            <strong>Prompt Preview:</strong>
        </p>
        <pre className="max-h-28 overflow-y-auto rounded bg-(--secondary-w10) p-1.5 font-mono text-xs break-all whitespace-pre-wrap">{data.promptPreview}</pre>
    </div>
);

const renderToolDecisionData = (data: ToolDecisionData): ReactNode => (
    <div className="mt-1.5 rounded-md bg-(--info-w10) p-2 font-mono text-xs text-(--info-wMain)">
        <p className="mb-2">
            <strong>🔧 {data.isParallel ? "Parallel Tool Calls:" : "Tool Call:"}</strong>
        </p>
        <ul className="space-y-1 pl-2">
            {data.decisions.map(decision => (
                <li key={decision.functionCallId} className="flex min-w-0 items-center">
                    <span className="mr-2 flex-shrink-0">•</span>
                    <code className="min-w-0 truncate" title={decision.toolName}>
                        {decision.toolName}
                    </code>
                </li>
            ))}
        </ul>
    </div>
);

const renderToolInvocationStartData = (data: ToolInvocationStartData): ReactNode => (
    <div className="mt-1.5 rounded-md bg-(--background-w10) p-2 text-xs text-(--primary-text-wMain)">
        <p>
            <strong>Tool:</strong> {data.toolName}
        </p>
        <p className="mt-1">
            <strong>Arguments:</strong>
        </p>
        <div className="max-h-40 overflow-y-auto rounded bg-(--secondary-w10) p-1.5">
            <JSONViewer data={data.toolArguments} />
        </div>
    </div>
);

const renderResultData = (resultData: unknown): ReactNode => {
    if (typeof resultData === "object") {
        return <JSONViewer data={resultData as Parameters<typeof JSONViewer>[0]["data"]} />;
    }
    return <pre className="font-mono text-xs break-all whitespace-pre-wrap">{String(resultData)}</pre>;
};

const renderToolResultData = (data: ToolResultData): ReactNode => {
    const { parsedResult, hasImages } = parseWebSearchResult(data.resultData);

    let resultBody: ReactNode;
    if (hasImages && parsedResult?.result) {
        resultBody = (
            <>
                <p className="mt-1">
                    <strong>Image Results:</strong>
                </p>
                <ImageSearchGrid images={isString(parsedResult.result) ? JSON.parse(parsedResult.result).images : parsedResult.result.images} />
                <details className="mt-2">
                    <summary className="cursor-pointer text-xs text-(--info-wMain) hover:text-(--info-w100)">Show full result data</summary>
                    <div className="mt-2 max-h-40 overflow-y-auto rounded bg-(--secondary-w10) p-1.5">{renderResultData(data.resultData)}</div>
                </details>
            </>
        );
    } else {
        resultBody = (
            <>
                <p className="mt-1">
                    <strong>Result:</strong>
                </p>
                <div className="max-h-40 overflow-y-auto rounded bg-(--secondary-w10) p-1.5">{renderResultData(data.resultData)}</div>
            </>
        );
    }

    return (
        <div className="mt-1.5 rounded-md bg-(--background-w10) p-2 text-xs text-(--primary-text-wMain)">
            <p>
                <strong>Tool:</strong> {data.toolName}
            </p>

            {resultBody}
        </div>
    );
};

interface ArtifactNotificationDeps {
    data: ArtifactNotificationData;
    artifactLookup?: (filename: string) => ArtifactInfo | undefined;
    artifacts: ArtifactInfo[];
    onViewArtifact?: (artifact: ArtifactInfo, version?: number) => void;
    setActiveSidePanelTab: ReturnType<typeof useChatContext>["setActiveSidePanelTab"];
    setIsSidePanelCollapsed: ReturnType<typeof useChatContext>["setIsSidePanelCollapsed"];
    setPreviewArtifact: ReturnType<typeof useChatContext>["setPreviewArtifact"];
    navigateArtifactVersion: ReturnType<typeof useChatContext>["navigateArtifactVersion"];
}

const renderArtifactNotificationData = (deps: ArtifactNotificationDeps): ReactNode => {
    const { data, artifactLookup, artifacts, onViewArtifact, setActiveSidePanelTab, setIsSidePanelCollapsed, setPreviewArtifact, navigateArtifactVersion } = deps;

    const handleViewFile = async (e: MouseEvent) => {
        e.stopPropagation();

        const artifact = artifactLookup ? artifactLookup(data.artifactName) : artifacts.find(a => a.filename === data.artifactName);
        if (!artifact) return;

        if (onViewArtifact) {
            onViewArtifact(artifact, data.version);
            return;
        }

        setActiveSidePanelTab("files");
        setIsSidePanelCollapsed(false);
        setPreviewArtifact(artifact);

        if (data.version !== undefined && data.version !== artifact.version) {
            setTimeout(() => {
                navigateArtifactVersion(artifact.filename, data.version!);
            }, 100);
        }
    };

    return (
        <div className="mt-1.5 rounded-md bg-(--background-w10) p-2 text-xs text-(--primary-text-wMain)">
            <div className="flex items-center justify-between">
                <p>
                    <strong>Artifact:</strong> {data.artifactName}
                    {data.version !== undefined && <span className="text-(--secondary-text-wMain)"> (v{data.version})</span>}
                </p>
                <button onClick={handleViewFile} className="flex items-center gap-1 text-(--info-wMain) transition-colors hover:text-(--info-w100)" title="View in Files tab">
                    <span className="text-xs">View File</span>
                    <ExternalLink size={12} />
                </button>
            </div>
            {data.mimeType && (
                <p>
                    <strong>Type:</strong> {data.mimeType}
                </p>
            )}
            {data.description && (
                <p className="mt-1">
                    <strong>Description:</strong> {data.description}
                </p>
            )}
        </div>
    );
};

const renderWorkflowNodeStartData = (data: WorkflowNodeExecutionStartData): ReactNode => (
    <div className="mt-1.5 rounded-md bg-(--background-w10) p-2 text-xs text-(--primary-text-wMain)">
        <div className="mb-1 flex items-center justify-between">
            <span className="text-[10px] font-bold text-(--secondary-text-wMain) uppercase">{data.nodeType} Node</span>
            {data.iterationIndex !== undefined && data.iterationIndex !== null && typeof data.iterationIndex === "number" && <span className="rounded bg-(--info-w10) px-1.5 py-0.5 text-[10px] text-(--info-wMain)">Iter #{data.iterationIndex}</span>}
        </div>

        {data.condition && (
            <div className="mt-1">
                <p className="mb-0.5 font-semibold">Condition:</p>
                <code className="block rounded border border-(--secondary-w20) bg-(--secondary-w10) p-1.5 font-mono text-xs break-all">{data.condition}</code>
            </div>
        )}
        {data.trueBranch && (
            <p className="mt-1">
                <strong>True Branch:</strong> {data.trueBranch}
            </p>
        )}
        {data.falseBranch && (
            <p>
                <strong>False Branch:</strong> {data.falseBranch}
            </p>
        )}
    </div>
);

const renderDecisionResultData = (decision: DecisionResult): ReactNode => (
    <>
        {decision.answers.map(answer => (
            <p key={answer.key} className="mt-1">
                <strong>{answer.key}:</strong> {decisionAnswerSummary(answer)}
            </p>
        ))}
        {decisionRoute(decision) && (
            <p className="mt-1">
                <strong>Selected Branch:</strong> {decisionRoute(decision)}
            </p>
        )}
        {decision.escalated && (
            <p className="mt-1 font-semibold text-(--warning-wMain)">
                {decisionEscalationLabel(decision)}
                {decision.providerError && ` (${decision.providerError})`}
            </p>
        )}
    </>
);

const renderBranchSelection = (metadata: WorkflowNodeExecutionResultData["metadata"]): ReactNode => {
    const decision = decisionResultOf(metadata);
    if (decision) return renderDecisionResultData(decision);
    if (metadata?.selected_branch === undefined) return null;
    return (
        <p className="mt-1">
            <strong>Selected Branch:</strong> {String(metadata.selected_branch)}
        </p>
    );
};

const renderWorkflowNodeResultData = (data: WorkflowNodeExecutionResultData): ReactNode => (
    <div className="mt-1.5 rounded-md bg-(--background-w10) p-2 text-xs text-(--primary-text-wMain)">
        <p>
            <strong>Status:</strong> {data.status}
        </p>
        {data.metadata?.condition && (
            <div className="mt-1 mb-1">
                <p className="mb-0.5 font-semibold">Condition:</p>
                <code className="block rounded border border-(--secondary-w20) bg-(--secondary-w10) p-1.5 font-mono text-xs break-all">{data.metadata.condition}</code>
            </div>
        )}
        {data.metadata?.condition_result !== undefined && (
            <p className="mt-1">
                <strong>Condition Result:</strong> <span className={data.metadata.condition_result ? "font-bold text-(--success-wMain)" : "font-bold text-(--warning-wMain)"}>{data.metadata.condition_result ? "True" : "False"}</span>
            </p>
        )}
        {renderBranchSelection(data.metadata)}
        {data.metadata?.selected_branch === undefined && typeof data.metadata?.cases_unresolved === "number" && (
            <p className="mt-1 text-(--warning-wMain)">
                <strong>No branch selected</strong>
            </p>
        )}
        {typeof data.metadata?.cases_unresolved === "number" && data.metadata.cases_unresolved > 0 && (
            <p className="mt-1 text-(--warning-wMain)">
                <strong>Unresolved cases:</strong> {data.metadata.cases_unresolved} referenced a field no node produced
            </p>
        )}
        {data.metadata?.stopped_reason === "condition_unresolved" && (
            <p className="mt-1 text-(--warning-wMain)">
                <strong>Loop stopped:</strong> its condition referenced a field no node produced
            </p>
        )}
        {data.metadata?.skip_reason !== undefined && (
            <p className={`mt-1 ${data.metadata?.unresolved_reference ? "text-(--warning-wMain)" : ""}`}>
                <strong>Skipped:</strong> {String(data.metadata.skip_reason)}
            </p>
        )}
        {data.outputArtifactRef && (
            <p className="mt-1">
                <strong>Output:</strong> {data.outputArtifactRef.name} (v{data.outputArtifactRef.version})
            </p>
        )}
        {data.errorMessage && (
            <p className="mt-1 text-(--error-w100)">
                <strong>Error:</strong> {data.errorMessage}
            </p>
        )}
    </div>
);

const renderWorkflowExecutionStartData = (data: WorkflowExecutionStartData): ReactNode => (
    <div className="mt-1.5 rounded-md bg-(--background-w10) p-2 text-xs text-(--primary-text-wMain)">
        <p>
            <strong>Workflow:</strong> {data.workflowName}
        </p>
        {data.workflowInput && (
            <div className="mt-1">
                <p>
                    <strong>Input:</strong>
                </p>
                <div className="max-h-40 overflow-y-auto rounded bg-(--secondary-w10) p-1.5">
                    <JSONViewer data={data.workflowInput} />
                </div>
            </div>
        )}
    </div>
);

const renderWorkflowExecutionResultData = (data: WorkflowExecutionResultData): ReactNode => (
    <div className="mt-1.5 rounded-md bg-(--background-w10) p-2 text-xs text-(--primary-text-wMain)">
        <p>
            <strong>Status:</strong> {data.status}
        </p>
        {data.workflowOutput && (
            <div className="mt-1">
                <p>
                    <strong>Output:</strong>
                </p>
                <div className="max-h-60 overflow-y-auto rounded bg-(--secondary-w10) p-1.5">
                    <JSONViewer data={data.workflowOutput} />
                </div>
            </div>
        )}
        {data.errorMessage && (
            <p className="text-(--error-w100)">
                <strong>Error:</strong> {data.errorMessage}
            </p>
        )}
    </div>
);

const VisualizerStepCard: FC<VisualizerStepCardProps> = ({ step, isHighlighted, onClick, variant = "list", artifactLookup, onViewArtifact }) => {
    const { setPreviewArtifact, setActiveSidePanelTab, setIsSidePanelCollapsed, navigateArtifactVersion } = useChatContext();
    const { artifacts } = useFilteredChatArtifacts();

    const formattedTimestamp = new Date(step.timestamp).toLocaleTimeString([], {
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
    });
    const milliseconds = String(new Date(step.timestamp).getMilliseconds()).padStart(3, "0");
    const displayTimestamp = `${formattedTimestamp}.${milliseconds}`;

    const indentationStyle = getIndentationStyle(variant, step.nestingLevel);
    const cardClasses = getCardClasses(variant, isHighlighted, !!onClick);

    return (
        <div className={cardClasses} style={indentationStyle} onClick={onClick} onKeyDown={onClick ? handleActivationKeyDown(onClick) : undefined} role={onClick ? "button" : undefined} tabIndex={onClick ? 0 : undefined}>
            <div className="mb-1.5 flex w-full items-center gap-1">
                {getStepIcon(step)}
                <div className="flex min-w-0 flex-1 flex-wrap items-center justify-between gap-2">
                    <h4 className="flex-1 truncate text-sm font-semibold" title={step.title}>
                        {step.title}
                    </h4>
                    <span className="shrink-0 font-mono text-xs text-(--secondary-text-wMain)">{displayTimestamp}</span>
                </div>
            </div>
            {step.delegationInfo && step.delegationInfo.length > 0 && (
                <div className="mt-2 mb-1.5 space-y-2 rounded-r-md border-l-4 border-(--info-wMain) bg-(--info-w10) p-2 text-sm">
                    {step.delegationInfo.map(info => (
                        <div key={info.functionCallId}>
                            <div className="flex min-w-0 items-center font-semibold text-(--info-wMain)">
                                <Link className="mr-2 h-4 w-4 flex-shrink-0" />
                                <span className="min-w-0 truncate" title={`${getDelegationText(step.type)}${info.peerAgentName}`}>
                                    {getDelegationText(step.type)}
                                    {info.peerAgentName}
                                </span>
                            </div>
                            {info.subTaskId && (
                                <div className="mt-0.5 ml-[24px] text-xs text-(--info-wMain)">
                                    Sub-Task:{" "}
                                    <span className="font-mono" title={info.subTaskId}>
                                        {info.subTaskId.substring(0, 15)}...
                                    </span>
                                </div>
                            )}
                        </div>
                    ))}
                </div>
            )}
            {step.data.text && (
                <div className="max-h-20 overflow-y-auto pl-1 text-sm text-(--primary-text-wMain)">
                    <MarkdownHTMLConverter>{step.data.text}</MarkdownHTMLConverter>
                </div>
            )}
            {step.data.finalMessage && (
                <div className="pl-1 text-sm text-(--primary-text-wMain)">
                    <MarkdownHTMLConverter>{step.data.finalMessage}</MarkdownHTMLConverter>
                </div>
            )}
            {step.type === "TASK_COMPLETED" && !step.data.finalMessage && <div className="pl-1 text-sm text-(--secondary-text-wMain) italic">Task completed successfully.</div>}
            {step.data.errorDetails && (
                <div className="mt-1 rounded-md bg-(--error-w10) p-2 pl-1 text-sm text-(--error-w100)">
                    <p>
                        <strong>Error:</strong> {step.data.errorDetails.message}
                    </p>
                    {step.data.errorDetails.code && <p className="text-xs">Code: {step.data.errorDetails.code}</p>}
                </div>
            )}
            {step.data.llmCall && renderLLMCallData(step.data.llmCall)}
            {step.data.llmResponseToAgent && <LLMResponseToAgentDetails data={step.data.llmResponseToAgent} />}
            {step.data.toolDecision && renderToolDecisionData(step.data.toolDecision)}
            {step.data.toolInvocationStart && renderToolInvocationStartData(step.data.toolInvocationStart)}
            {step.data.toolResult && renderToolResultData(step.data.toolResult)}
            {step.data.artifactNotification &&
                renderArtifactNotificationData({
                    data: step.data.artifactNotification,
                    artifactLookup,
                    artifacts,
                    onViewArtifact,
                    setActiveSidePanelTab,
                    setIsSidePanelCollapsed,
                    setPreviewArtifact,
                    navigateArtifactVersion,
                })}
            {step.data.workflowExecutionStart && renderWorkflowExecutionStartData(step.data.workflowExecutionStart)}
            {step.data.workflowNodeExecutionStart && renderWorkflowNodeStartData(step.data.workflowNodeExecutionStart)}
            {step.data.workflowNodeExecutionResult && renderWorkflowNodeResultData(step.data.workflowNodeExecutionResult)}
            {step.data.workflowExecutionResult && renderWorkflowExecutionResultData(step.data.workflowExecutionResult)}
        </div>
    );
};

export { VisualizerStepCard };
