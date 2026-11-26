// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useEffect, useState } from "react";
import { ArrowRight, Bot, CircleCheckBig, FileText, GitBranch, Loader2, RefreshCw, Signpost, Terminal, TriangleAlert, User, Workflow, Wrench, Zap } from "lucide-react";

import { useFilteredChatArtifacts } from "@/api/artifacts";

import { useChatContext } from "@/providers/contexts";

import { type JSONValue, JSONViewer } from "@/common/JSONViewer";
import { MarkdownHTMLConverter } from "@/common/markdown";

import { decisionAnswerSummary, decisionEscalationLabel, type DecisionResult, decisionResultOf } from "@/features/activities/decisionResult";
import { ARTIFACT_CARD_STYLES, NODE_ACCENT_COLOR, NODE_COLORS, PEER_BADGE_STYLES } from "@/features/activities/nodeColors";
import { ContentRenderer, decodeBase64Content, encodeBase64Content, getFileContent, getRenderType, RENDER_TYPES_WITH_RAW_CONTENT, RENDER_TYPES_WITH_URL_ONLY } from "@/features/chat/preview";

import type { FileAttachment, ToolDecision, VisualizerStep } from "@/types";
import { cn, getArtifactContent, getArtifactUrl, parseArtifactUri } from "@/utils";

import { useArtifactSession } from "./artifactSessionContext";
import type { NodeDetails } from "./utils/nodeDetailsHelper";
import { isFailedResultStatus } from "./utils/resultStatus";

const MAX_ARTIFACT_DISPLAY_LENGTH = 1000;

interface InlineOutput {
    name: string;
    content: string;
    truncated?: boolean;
}

const INLINE_OUTPUTS_KEY = "inline_outputs";

const isInlineOutputList = (value: unknown): value is InlineOutput[] =>
    Array.isArray(value) && value.length > 0 && value.every(item => typeof item === "object" && item !== null && typeof (item as InlineOutput).content === "string" && typeof (item as InlineOutput).name === "string");

const ColumnHeader = ({ label, color }: { label: string; color: string }) => {
    return (
        <div className="mb-3 flex items-center gap-2 border-b pb-2">
            <div className={`h-2 w-2 rounded-full bg-${color}`}></div>
            <h4 className={`text-sm font-bold text-${color}`}>{label}</h4>
        </div>
    );
};

interface ArtifactContentViewerProps {
    uri?: string;
    name: string;
    version?: number;
    mimeType?: string;
}

const resultStatusClass = (status: string): string => {
    if (status === "success") return "text-(--success-wMain)";
    if (isFailedResultStatus(status)) return "text-(--error-w100)";
    return "";
};

const isNotTruncatable = (renderType: string) => renderType === "html" || RENDER_TYPES_WITH_RAW_CONTENT.includes(renderType) || RENDER_TYPES_WITH_URL_ONLY.includes(renderType);

export const ArtifactContentViewer = ({ uri, name, version, mimeType }: ArtifactContentViewerProps) => {
    const { sessionId: chatSessionId } = useChatContext();
    const overrideSessionId = useArtifactSession();
    const sessionId = overrideSessionId ?? chatSessionId;
    const [content, setContent] = useState<string | null>(null);
    const [fetchedMimeType, setFetchedMimeType] = useState<string | null>(null);
    const [previewUrl, setPreviewUrl] = useState<string | null>(null);
    const [resolvedName, setResolvedName] = useState<string | null>(null);
    const [isLoading, setIsLoading] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const [isTruncated, setIsTruncated] = useState(false);

    useEffect(() => {
        const fetchContent = async () => {
            setContent(null);
            setPreviewUrl(null);
            setResolvedName(null);
            setIsTruncated(false);
            if ((!sessionId && !uri) || !name) return;

            setIsLoading(true);
            setError(null);

            try {
                let parsedFileName;
                let parsedSessionId;
                let parsedVersion;

                if (uri) {
                    const parsed = parseArtifactUri(uri);
                    if (parsed) {
                        parsedFileName = parsed.filename;
                        parsedSessionId = parsed.sessionId;
                        if (parsed.version) {
                            const uriVersion = parseInt(parsed.version, 10);
                            parsedVersion = Number.isInteger(uriVersion) ? uriVersion : undefined;
                        }
                    }
                }

                const effectiveFilename = parsedFileName ?? name;
                const effectiveSessionId = parsedSessionId ?? sessionId;
                const effectiveVersion = parsedVersion ?? version ?? "latest";
                setResolvedName(effectiveFilename);

                const { content, mimeType: responseMimeType } = await getArtifactContent({
                    filename: effectiveFilename,
                    sessionId: effectiveSessionId,
                    version: effectiveVersion,
                });
                setFetchedMimeType(responseMimeType);

                setPreviewUrl(getArtifactUrl({ filename: effectiveFilename, sessionId: effectiveSessionId, version: effectiveVersion }));

                const currentRenderType = getRenderType(effectiveFilename, responseMimeType);
                const shouldTruncate = currentRenderType && !isNotTruncatable(currentRenderType);

                if (shouldTruncate) {
                    const decodedText = decodeBase64Content(content);
                    if (decodedText.length > MAX_ARTIFACT_DISPLAY_LENGTH) {
                        const truncatedText = decodedText.substring(0, MAX_ARTIFACT_DISPLAY_LENGTH);
                        setContent(encodeBase64Content(truncatedText));
                        setIsTruncated(true);
                    } else {
                        setContent(content);
                        setIsTruncated(false);
                    }
                } else {
                    setContent(content);
                    setIsTruncated(false);
                }
            } catch (err) {
                console.error("Error fetching artifact:", err);
                setError(err instanceof Error ? err.message : "Failed to load artifact");
            } finally {
                setIsLoading(false);
            }
        };

        fetchContent();
    }, [uri, name, version, sessionId]);

    if (isLoading) {
        return (
            <div className="flex items-center gap-2 text-xs">
                <Loader2 className="h-3 w-3 animate-spin" />
                Loading artifact content...
            </div>
        );
    }

    if (error) {
        return <div className="text-xs text-(--error-w100)">{error}</div>;
    }

    if (!content) {
        return <div className="text-xs text-(--secondary-text-wMain)">No content available</div>;
    }

    const effectiveMimeType = fetchedMimeType || mimeType;
    const displayName = resolvedName ?? name;
    const renderType = getRenderType(displayName, effectiveMimeType);

    if (!renderType) {
        return <div className="text-xs text-(--secondary-text-wMain)">Preview not available for this file type</div>;
    }

    const fileAttachment: FileAttachment = {
        name: displayName,
        mimeType: effectiveMimeType,
        content,
    };
    const processedContent = getFileContent(fileAttachment);

    if (!processedContent) {
        return <div className="text-xs text-(--secondary-text-wMain)">No content available</div>;
    }

    return (
        <>
            <div className={`overflow-y-auto ${renderType === "html" ? "h-[calc(50vh-100px)]" : "max-h-[calc(50vh-100px)]"}`}>
                <ContentRenderer content={processedContent} rendererType={renderType} mimeType={effectiveMimeType} url={previewUrl ?? undefined} filename={displayName} setRenderError={setError} />
            </div>
            {isTruncated && <div className="mt-2 text-xs text-(--info-wMain)">Content truncated at first {MAX_ARTIFACT_DISPLAY_LENGTH.toLocaleString()} characters</div>}
        </>
    );
};

interface NodeDetailsCardProps {
    nodeDetails: NodeDetails;
    onClose?: () => void;
    onWidthChange?: (isExpanded: boolean) => void;
}

const NodeDetailsCard = ({ nodeDetails, onClose }: NodeDetailsCardProps) => {
    const { setPreviewArtifact: setSidePanelPreviewArtifact, setActiveSidePanelTab, setIsSidePanelCollapsed, navigateArtifactVersion } = useChatContext();
    const { artifacts } = useFilteredChatArtifacts();
    const overrideSessionId = useArtifactSession();

    const getNodeIcon = () => {
        switch (nodeDetails.nodeType) {
            case "user":
                return <User className={NODE_COLORS.user} size={20} />;
            case "agent":
                return <Bot className="text-(--brand-wMain)" size={20} />;
            case "llm":
                return <Zap className={NODE_COLORS.llm} size={20} />;
            case "tool":
                return <Wrench className={NODE_COLORS.tool} size={20} />;
            case "switch":
                return <GitBranch className={NODE_COLORS.switch} size={20} />;
            case "decision":
                return <Signpost className={NODE_COLORS.decision} size={20} />;
            case "loop":
                return <RefreshCw className={NODE_COLORS.loop} size={20} />;
            case "group":
                return <Workflow className={NODE_COLORS.group} size={20} />;
            default:
                return <Terminal className="text-(--secondary-text-wMain)" size={20} />;
        }
    };

    const renderStepContent = (step: VisualizerStep | undefined, isRequest: boolean) => {
        if (!step) {
            return <div className="flex h-full items-center justify-center text-(--secondary-text-wMain)">{isRequest ? "No request data available" : "No result data available"}</div>;
        }

        const date = new Date(step.timestamp);
        const timeString = date.toLocaleTimeString([], {
            hour: "2-digit",
            minute: "2-digit",
            second: "2-digit",
            hour12: false,
        });
        const milliseconds = String(date.getMilliseconds()).padStart(3, "0");
        const formattedTimestamp = `${timeString}.${milliseconds}`;

        return (
            <div className="space-y-3">
                <div className="font-mono text-xs text-(--secondary-text-wMain)">{formattedTimestamp}</div>

                {renderStepTypeContent(step)}
            </div>
        );
    };

    const renderStepTypeContent = (step: VisualizerStep) => {
        switch (step.type) {
            case "USER_REQUEST":
                return renderUserRequest(step);
            case "WORKFLOW_AGENT_REQUEST":
                return renderWorkflowAgentRequest(step);
            case "AGENT_RESPONSE_TEXT":
                return renderAgentResponse(step);
            case "AGENT_LLM_CALL":
                return renderLLMCall(step);
            case "AGENT_LLM_RESPONSE_TO_AGENT":
                return renderLLMResponse(step);
            case "AGENT_LLM_RESPONSE_TOOL_DECISION":
                return renderLLMToolDecision(step);
            case "AGENT_TOOL_INVOCATION_START":
                return renderToolInvocation(step);
            case "AGENT_TOOL_EXECUTION_RESULT":
                return renderToolResult(step);
            case "WORKFLOW_NODE_EXECUTION_START":
                if (step.data.workflowNodeExecutionStart?.nodeType === "agent") {
                    return renderWorkflowAgentInvocation(step);
                }
                return renderWorkflowNodeStart(step);
            case "WORKFLOW_NODE_EXECUTION_RESULT":
                return renderWorkflowNodeResult(step);
            case "WORKFLOW_EXECUTION_START":
                return renderWorkflowStart(step);
            case "WORKFLOW_EXECUTION_RESULT":
                return renderWorkflowResult(step);
            default:
                return <div className="text-sm">{step.title}</div>;
        }
    };

    const renderUserRequest = (step: VisualizerStep) => (
        <div>
            <h4 className="mb-2 text-sm font-semibold">User Input</h4>
            {step.data.text && (
                <div className="prose prose-sm max-h-96 max-w-none overflow-y-auto rounded-md bg-(--secondary-w10) p-3">
                    <MarkdownHTMLConverter>{step.data.text}</MarkdownHTMLConverter>
                </div>
            )}
        </div>
    );

    const renderWorkflowAgentRequest = (step: VisualizerStep) => {
        const data = step.data.workflowAgentRequest;
        if (!data) return null;

        return (
            <div>
                <h4 className="mb-2 text-sm font-semibold">Workflow Agent Request</h4>
                <div className="space-y-3">
                    {data.nodeId && (
                        <div className="text-xs">
                            <span className="font-semibold">Node Id:</span> <span>{data.nodeId}</span>
                        </div>
                    )}

                    {data.instruction && (
                        <div>
                            <div className="mb-1 text-xs font-semibold">Instruction:</div>
                            <div className="prose prose-sm max-w-none overflow-y-auto rounded-md border border-(--info-w100) bg-(--info-w10) p-3">
                                <MarkdownHTMLConverter>{data.instruction}</MarkdownHTMLConverter>
                            </div>
                        </div>
                    )}

                    {data.inputArtifactRef && (
                        <div>
                            <div className="mb-1 flex min-w-0 items-baseline gap-1 text-xs font-semibold">
                                <span className="flex-shrink-0">Input:</span>
                                <span className="min-w-0 truncate font-normal text-(--secondary-text-wMain)" title={data.inputArtifactRef.name}>
                                    {data.inputArtifactRef.name}
                                </span>
                                {data.inputArtifactRef.version !== undefined && <span className={cn("ml-1 shrink-0", NODE_ACCENT_COLOR)}>v{data.inputArtifactRef.version}</span>}
                            </div>

                            <ArtifactContentViewer uri={data.inputArtifactRef.uri} name={data.inputArtifactRef.name} version={data.inputArtifactRef.version} />
                        </div>
                    )}

                    {data.inputText && !data.inputArtifactRef && (
                        <div>
                            <div className="mb-1 text-xs font-semibold">Input:</div>
                            <div className="prose prose-sm max-w-none overflow-y-auto rounded-md bg-(--secondary-w10) p-3">
                                <MarkdownHTMLConverter>{data.inputText}</MarkdownHTMLConverter>
                            </div>
                        </div>
                    )}

                    {data.inputSchema && (
                        <div>
                            <div className="mb-1 text-xs font-semibold">Input Schema:</div>
                            <div className="my-1 max-h-48 overflow-y-auto py-2">
                                <JSONViewer data={data.inputSchema} maxDepth={0} />
                            </div>
                        </div>
                    )}

                    {data.outputSchema && (
                        <div>
                            <div className="mb-1 text-xs font-semibold">Output Schema:</div>
                            <div className="my-1 max-h-48 overflow-y-auto py-2">
                                <JSONViewer data={data.outputSchema} maxDepth={0} />
                            </div>
                        </div>
                    )}

                    {!data.inputText && !data.inputArtifactRef && !data.instruction && <div className="text-xs text-(--secondary-text-wMain) italic">No input data available</div>}
                </div>
            </div>
        );
    };

    const renderAgentResponse = (step: VisualizerStep) => (
        <div>
            <h4 className="mb-2 text-sm font-semibold">Agent Response</h4>
            {step.data.text && (
                <div className="prose prose-sm max-w-none overflow-y-auto rounded-md bg-(--secondary-w10) p-3">
                    <MarkdownHTMLConverter>{step.data.text}</MarkdownHTMLConverter>
                </div>
            )}
        </div>
    );

    const renderLLMCall = (step: VisualizerStep) => {
        const data = step.data.llmCall;
        if (!data) return null;

        return (
            <div>
                <h4 className="mb-2 text-sm font-semibold">LLM Request</h4>
                <div className="space-y-2">
                    <div className="text-xs">
                        <span className="font-semibold">Model:</span> {data.modelName}
                    </div>
                    <div>
                        <div className="mb-1 text-xs font-semibold">Prompt:</div>
                        <pre className="max-h-80 overflow-auto rounded-md bg-(--secondary-w10) p-2 text-xs break-words whitespace-pre-wrap">{data.promptPreview}</pre>
                    </div>
                </div>
            </div>
        );
    };

    const renderLLMResponse = (step: VisualizerStep) => {
        const data = step.data.llmResponseToAgent;
        if (!data) return null;

        return (
            <div>
                <h4 className="mb-2 text-sm font-semibold">LLM Response</h4>
                <div className="space-y-2">
                    {data.modelName && (
                        <div className="text-xs">
                            <span className="font-semibold">Model:</span> {data.modelName}
                        </div>
                    )}
                    <div>
                        <pre className="max-h-80 overflow-auto rounded-md bg-(--secondary-w10) p-2 text-xs break-words whitespace-pre-wrap">{data.response || data.responsePreview}</pre>
                    </div>
                    {data.isFinalResponse !== undefined && (
                        <div className="text-xs">
                            <span className="font-semibold">Final Response:</span> {data.isFinalResponse ? "Yes" : "No"}
                        </div>
                    )}
                </div>
            </div>
        );
    };

    const renderLLMToolDecision = (step: VisualizerStep) => {
        const data = step.data.toolDecision;
        if (!data) return null;

        return (
            <div>
                <h4 className="mb-2 text-sm font-semibold">LLM Tool Decision{data.isParallel ? " (Parallel)" : ""}</h4>
                <div className="space-y-3">
                    {data.decisions && data.decisions.length > 0 && (
                        <div>
                            <div className="mb-2 text-xs font-semibold">Tools to invoke:</div>
                            <div className="space-y-2">
                                {data.decisions.map((decision: ToolDecision, index: number) => (
                                    <div key={index} className="rounded-md border p-2">
                                        <div className="mb-1 text-xs font-semibold text-(--info-wMain)">
                                            {decision.toolName}
                                            {decision.isPeerDelegation && (
                                                <span className={cn("ml-2 rounded px-1.5 py-0.5 text-xs", PEER_BADGE_STYLES.bg, PEER_BADGE_STYLES.text)}>{decision.toolName.startsWith("workflow_") ? "Workflow" : "Peer Agent"}</span>
                                            )}
                                        </div>
                                        {decision.toolArguments && Object.keys(decision.toolArguments).length > 0 && <div className="mt-1">{renderFormattedArguments(decision.toolArguments)}</div>}
                                    </div>
                                ))}
                            </div>
                        </div>
                    )}
                </div>
            </div>
        );
    };

    const renderToolInvocation = (step: VisualizerStep) => {
        const data = step.data.toolInvocationStart;
        if (!data) return null;

        return (
            <div>
                <h4 className="mb-2 text-sm font-semibold">{data.isPeerInvocation ? "Peer Agent Call" : "Tool Invocation"}</h4>
                <div className="space-y-2">
                    <div className="text-xs">
                        <span className="font-semibold">Tool:</span> {data.toolName}
                    </div>
                    <div>
                        <div className="mb-2 text-xs font-semibold">Arguments:</div>
                        {renderFormattedArguments(data.toolArguments)}
                    </div>
                </div>
            </div>
        );
    };

    const renderFormattedArguments = (args: Record<string, unknown> | null | undefined) => {
        const entries = args ? Object.entries(args) : [];

        if (entries.length === 0) {
            return <div className="text-xs text-(--secondary-text-wMain) italic">No arguments</div>;
        }

        return (
            <div className="space-y-3">
                {entries.map(([key, value]) => (
                    <div key={key} className="overflow-hidden rounded-md border p-2">
                        <div className="mb-1 text-xs font-semibold text-(--info-wMain)">{key}</div>
                        <div className="max-h-60 overflow-auto text-xs">{key === INLINE_OUTPUTS_KEY && isInlineOutputList(value) ? renderInlineOutputs(value) : renderArgumentValue(value)}</div>
                    </div>
                ))}
            </div>
        );
    };

    const renderInlineOutputs = (outputs: InlineOutput[]): ReactNode => (
        <div className="space-y-2">
            {outputs.map((output, idx) => (
                <div key={output.name || idx} className="border-l-2 border-(--info-wMain) pl-2">
                    <div className="mb-0.5 flex items-center gap-2">
                        <span className="font-semibold">{output.name || `output ${idx + 1}`}</span>
                        {output.truncated && <span className="text-(--secondary-text-wMain) italic">truncated</span>}
                    </div>
                    <span className="break-words whitespace-pre-wrap">{output.content}</span>
                </div>
            ))}
        </div>
    );

    const renderArgumentValue = (value: unknown): ReactNode => {
        if (value === null) {
            return <span className="text-(--secondary-text-wMain) italic">null</span>;
        }
        if (value === undefined) {
            return <span className="text-(--secondary-text-wMain) italic">undefined</span>;
        }

        if (typeof value === "string") {
            return <span className="break-words whitespace-pre-wrap">{value}</span>;
        }
        if (typeof value === "number") {
            return <span className={NODE_ACCENT_COLOR}>{value}</span>;
        }
        if (typeof value === "boolean") {
            return <span className="text-(--success-wMain)">{value.toString()}</span>;
        }

        if (Array.isArray(value)) {
            if (value.length === 0) {
                return <span className="text-(--secondary-text-wMain) italic">[]</span>;
            }
            if (value.every(item => typeof item === "string" || typeof item === "number" || typeof item === "boolean")) {
                return (
                    <div className="space-y-1">
                        {value.map((item, idx) => (
                            <div key={idx} className="border-l-2 border-(--info-wMain) pl-2">
                                {renderArgumentValue(item)}
                            </div>
                        ))}
                    </div>
                );
            }
            return (
                <div className="my-1">
                    <JSONViewer data={value} maxDepth={1} />
                </div>
            );
        }

        if (typeof value === "object") {
            const entries = value ? Object.entries(value) : [];

            if (entries.length <= 5 && entries.every(([_, v]) => typeof v === "string" || typeof v === "number" || typeof v === "boolean" || v === null)) {
                return (
                    <div className="space-y-1">
                        {entries.map(([k, v]) => (
                            <div key={k} className="flex min-w-0 gap-2">
                                <span className="flex-shrink-0 font-semibold">{k}:</span>
                                <span className="min-w-0 break-words">{renderArgumentValue(v)}</span>
                            </div>
                        ))}
                    </div>
                );
            }

            return (
                <div className="my-1">
                    <JSONViewer data={value as JSONValue} maxDepth={1} />
                </div>
            );
        }

        return <span className="text-(--secondary-text-wMain)">{String(value)}</span>;
    };

    const renderToolResult = (step: VisualizerStep) => {
        const data = step.data.toolResult;
        if (!data) return null;

        return (
            <div>
                <h4 className="mb-2 text-sm font-semibold">{data.isPeerResponse ? "Peer Agent Result" : "Tool Result"}</h4>
                <div className="space-y-2">
                    <div className="text-xs">
                        <span className="font-semibold">Tool:</span> {data.toolName}
                    </div>
                    <div>
                        <div className="mb-2 text-xs font-semibold">Result:</div>
                        {renderResultValue(data.resultData)}
                    </div>
                </div>
            </div>
        );
    };

    const renderResultValue = (resultData: unknown) =>
        typeof resultData === "object" && resultData !== null ? (
            renderFormattedArguments(resultData as Record<string, unknown>)
        ) : (
            <div className="overflow-hidden rounded-md border bg-(--secondary-w10) p-2">
                <div className="max-h-60 overflow-auto text-xs">{renderArgumentValue(resultData)}</div>
            </div>
        );

    const renderWorkflowAgentInvocation = (step: VisualizerStep) => {
        const data = step.data.workflowNodeExecutionStart;
        if (!data) return null;

        return (
            <div>
                <h4 className="mb-2 text-sm font-semibold">Workflow Agent Invocation</h4>
                <div className="space-y-2">
                    <div className="text-xs">
                        <span className="font-semibold">Agent:</span> {data.agentName || data.nodeId}
                    </div>
                    <div className="text-xs">
                        <span className="font-semibold">Workflow Node:</span> {data.nodeId}
                    </div>
                    {data.iterationIndex !== undefined && data.iterationIndex !== null && typeof data.iterationIndex === "number" && (
                        <div className="inline-block rounded bg-(--info-w10) px-2 py-1 text-xs text-(--info-wMain)">Iteration #{data.iterationIndex}</div>
                    )}
                    {data.inputArtifactRef && (
                        <div className="mt-2">
                            <div className="mb-1 text-xs font-semibold">Input:</div>
                            <div className="rounded-md border bg-(--secondary-w10) p-2">
                                <div className="text-xs">
                                    <div className="mb-1 font-semibold text-(--info-wMain)">Artifact Reference</div>
                                    <div className="space-y-1">
                                        <div className="flex min-w-0 gap-2">
                                            <span className="flex-shrink-0 font-semibold">name:</span>
                                            <span className="truncate" title={data.inputArtifactRef.name}>
                                                {data.inputArtifactRef.name}
                                            </span>
                                        </div>
                                        {data.inputArtifactRef.version !== undefined && (
                                            <div className="flex gap-2">
                                                <span className="flex-shrink-0 font-semibold">version:</span>
                                                <span className={NODE_ACCENT_COLOR}>{data.inputArtifactRef.version}</span>
                                            </div>
                                        )}
                                    </div>
                                </div>
                            </div>
                        </div>
                    )}
                    <div className="mt-2 text-xs text-(--secondary-text-wMain) italic">This agent was invoked by the workflow with the input specified above.</div>
                </div>
            </div>
        );
    };

    const renderWorkflowNodeStart = (step: VisualizerStep) => {
        const data = step.data.workflowNodeExecutionStart;
        if (!data) return null;

        if (data.nodeType === "switch") {
            return (
                <div>
                    <h4 className="mb-2 text-sm font-semibold">Switch Node</h4>
                    <div className="space-y-3">
                        <div className="text-xs">
                            <span className="font-semibold">Node ID:</span> {data.nodeId}
                        </div>

                        {data.cases && data.cases.length > 0 && (
                            <div>
                                <div className="mb-2 text-xs font-semibold">Cases:</div>
                                <div className="space-y-2">
                                    {data.cases.map((caseItem, index) => (
                                        <div key={index} className="rounded-md border bg-(--secondary-w10) p-2">
                                            <div className="mb-1 flex items-center gap-2">
                                                <span className={cn("text-xs font-semibold", NODE_ACCENT_COLOR)}>Case {index + 1}</span>
                                                <ArrowRight className="h-3 w-3 text-(--secondary-text-wMain)" />
                                                <span className="text-xs font-medium text-(--info-wMain)">{caseItem.node}</span>
                                            </div>
                                            <code className="block text-xs break-all text-(--secondary-text-wMain)">{caseItem.condition}</code>
                                        </div>
                                    ))}
                                </div>
                            </div>
                        )}

                        {data.defaultBranch && (
                            <div className="rounded-md border border-(--accent-n6-w100) p-2">
                                <div className="flex items-center gap-2">
                                    <span className="text-xs font-semibold text-(--accent-n6-wMain)">Default</span>
                                    <ArrowRight className="h-3 w-3 text-(--secondary-text-wMain)" />
                                    <span className="text-xs font-medium text-(--info-wMain)">{data.defaultBranch}</span>
                                </div>
                            </div>
                        )}
                    </div>
                </div>
            );
        }

        if (data.nodeType === "decision") {
            return (
                <div>
                    <h4 className="mb-2 text-sm font-semibold">Decision Node</h4>
                    <div className="space-y-2">
                        <div className="text-xs">
                            <span className="font-semibold">Node ID:</span> {data.nodeId}
                        </div>
                        {data.dependsOn && data.dependsOn.length > 0 && (
                            <div className="text-xs">
                                <span className="font-semibold">Depends on:</span> {data.dependsOn.join(", ")}
                            </div>
                        )}
                    </div>
                </div>
            );
        }

        if (data.nodeType === "loop") {
            return (
                <div>
                    <h4 className="mb-2 text-sm font-semibold">Loop Node</h4>
                    <div className="space-y-2">
                        <div className="text-xs">
                            <span className="font-semibold">Node ID:</span> {data.nodeId}
                        </div>
                        {data.condition && (
                            <div>
                                <div className="mb-1 text-xs font-semibold">Condition:</div>
                                <code className="block rounded-md bg-(--secondary-w10) p-2 text-xs break-all">{data.condition}</code>
                            </div>
                        )}
                        {data.maxIterations !== undefined && (
                            <div className="text-xs">
                                <span className="font-semibold">Max Iterations:</span> {data.maxIterations}
                            </div>
                        )}
                        {data.loopDelay && (
                            <div className="text-xs">
                                <span className="font-semibold">Delay:</span> {data.loopDelay}
                            </div>
                        )}
                    </div>
                </div>
            );
        }

        return (
            <div>
                <h4 className="mb-2 text-sm font-semibold">Workflow Node Start</h4>
                <div className="space-y-2">
                    <div className="text-xs">
                        <span className="font-semibold">Node ID:</span> {data.nodeId}
                    </div>
                    <div className="text-xs">
                        <span className="font-semibold">Type:</span> {data.nodeType}
                    </div>
                    {data.agentName && (
                        <div className="text-xs">
                            <span className="font-semibold">Agent:</span> {data.agentName}
                        </div>
                    )}
                    {data.toolName && (
                        <div className="text-xs">
                            <span className="font-semibold">Tool:</span> {data.toolName}
                        </div>
                    )}
                    {data.toolArguments !== undefined && (
                        <div>
                            <div className="mb-2 text-xs font-semibold">Arguments:</div>
                            {renderFormattedArguments(data.toolArguments)}
                        </div>
                    )}
                    {data.condition && (
                        <div>
                            <div className="mb-1 text-xs font-semibold">Condition:</div>
                            <code className="block rounded-md bg-(--secondary-w10) p-2 text-xs break-all">{data.condition}</code>
                        </div>
                    )}
                    {data.iterationIndex !== undefined && data.iterationIndex !== null && typeof data.iterationIndex === "number" && (
                        <div className="inline-block rounded bg-(--info-w10) px-2 py-1 text-xs text-(--info-wMain)">Iteration #{data.iterationIndex}</div>
                    )}
                </div>
            </div>
        );
    };

    const renderDecisionResult = (status: string, decision: DecisionResult) => {
        return (
            <div>
                <h4 className="mb-2 text-sm font-semibold">Decision Result</h4>
                <div className="space-y-2">
                    <div className="text-xs">
                        <span className="font-semibold">Status:</span> <span className={status === "success" ? "text-(--success-wMain)" : ""}>{status}</span>
                    </div>

                    {decision.answers.length > 0 && (
                        <div>
                            <div className="mb-1 text-xs font-semibold">Answers:</div>
                            <div className="space-y-1">
                                {decision.answers.map(answer => (
                                    <div key={answer.key} className="flex items-baseline gap-2 rounded-md bg-(--secondary-w10) px-2 py-1 text-xs">
                                        <span className="font-semibold">{answer.key}</span>
                                        <span className="text-(--secondary-text-wMain)">{answer.type}</span>
                                        <span className="ml-auto font-medium">{decisionAnswerSummary(answer)}</span>
                                    </div>
                                ))}
                            </div>
                        </div>
                    )}

                    {decision.selectedBranch && (
                        <div className={cn("mt-2 rounded-md border p-2", decision.escalated ? "border-(--warning-wMain)" : "border-(--success-wMain)")}>
                            <div className="flex items-center gap-2">
                                {!decision.escalated && <CircleCheckBig className="h-4 w-4 text-(--success-wMain)" />}
                                <span className={cn("text-xs font-semibold", decision.escalated ? "text-(--warning-wMain)" : "text-(--success-wMain)")}>Selected Branch:</span>
                                <span className={cn("text-xs font-bold", decision.escalated ? "text-(--warning-wMain)" : "text-(--success-wMain)")}>{decision.selectedBranch}</span>
                            </div>
                        </div>
                    )}

                    {decision.escalated && (
                        <div className="flex items-center gap-1 rounded-md bg-(--warning-w10) px-2 py-1 text-xs font-semibold text-(--warning-wMain)">
                            <TriangleAlert className="h-3 w-3" />
                            {decisionEscalationLabel(decision)}
                        </div>
                    )}

                    {decision.providerError && (
                        <div className="text-xs text-(--warning-wMain)">
                            <span className="font-semibold">Provider error:</span> {decision.providerError}
                        </div>
                    )}
                    {decision.model && (
                        <div className="text-xs">
                            <span className="font-semibold">Model:</span> {decision.model}
                        </div>
                    )}
                    {decision.durationMs !== undefined && (
                        <div className="text-xs">
                            <span className="font-semibold">Duration:</span> {decision.durationMs} ms
                        </div>
                    )}
                </div>
            </div>
        );
    };

    const renderWorkflowNodeResult = (step: VisualizerStep) => {
        const data = step.data.workflowNodeExecutionResult;
        if (!data) return null;

        const decision = decisionResultOf(data.metadata);
        if (decision) return renderDecisionResult(data.status, decision);

        const selectedBranch = data.metadata?.selected_branch;
        const selectedCaseIndex = data.metadata?.selected_case_index;
        const rawCasesUnresolved = data.metadata?.cases_unresolved;
        const casesUnresolved = typeof rawCasesUnresolved === "number" ? rawCasesUnresolved : undefined;
        const isSwitch = selectedBranch !== undefined || selectedCaseIndex !== undefined || casesUnresolved !== undefined;

        return (
            <div>
                <h4 className="mb-2 text-sm font-semibold">{isSwitch ? "Switch Result" : "Workflow Node Result"}</h4>
                <div className="space-y-2">
                    <div className="text-xs">
                        <span className="font-semibold">Status:</span> <span className={resultStatusClass(data.status)}>{data.status}</span>
                    </div>

                    {selectedBranch !== undefined && (
                        <div className={`mt-2 rounded-md border p-2 ${casesUnresolved ? "border-(--warning-wMain)" : "border-(--success-wMain)"}`}>
                            <div className="flex items-center gap-2">
                                {!casesUnresolved && <CircleCheckBig className="h-4 w-4 text-(--success-wMain)" />}
                                <span className={`text-xs font-semibold ${casesUnresolved ? "text-(--warning-wMain)" : "text-(--success-wMain)"}`}>Selected Branch:</span>
                                <span className={`text-xs font-bold ${casesUnresolved ? "text-(--warning-wMain)" : "text-(--success-wMain)"}`}>{selectedBranch}</span>
                            </div>
                            {selectedCaseIndex !== undefined && selectedCaseIndex !== null && <div className={`mt-1 text-xs ${casesUnresolved ? "text-(--warning-wMain)" : "text-(--success-wMain)"}`}>Matched Case #{selectedCaseIndex + 1}</div>}
                            {selectedCaseIndex === null && <div className="mt-1 text-xs text-(--warning-wMain)">(Default branch - no case matched)</div>}
                        </div>
                    )}

                    {selectedBranch === undefined && casesUnresolved !== undefined && (
                        <div className="mt-2 rounded-md border border-(--warning-wMain) p-2">
                            <div className="text-xs font-semibold text-(--warning-wMain)">No branch selected</div>
                        </div>
                    )}

                    {casesUnresolved !== undefined && casesUnresolved > 0 && <div className="mt-1 text-xs text-(--warning-wMain)">{casesUnresolved} case(s) referenced a field no node produced</div>}

                    {data.metadata?.stopped_reason === "condition_unresolved" && <div className="mt-1 text-xs text-(--warning-wMain)">Loop stopped: its condition referenced a field no node produced</div>}

                    {typeof data.metadata?.skip_reason === "string" && (
                        <div className="text-xs">
                            <span className="font-semibold">Skipped:</span> {data.metadata.skip_reason}
                        </div>
                    )}

                    {data.conditionResult !== undefined && (
                        <div className="text-xs">
                            <span className="font-semibold">Condition Result:</span> <span className={data.conditionResult ? "font-bold text-(--success-wMain)" : "font-bold text-(--warning-wMain)"}>{data.conditionResult ? "True" : "False"}</span>
                        </div>
                    )}
                    {data.metadata?.condition && (
                        <div>
                            <div className="mb-1 text-xs font-semibold">Condition:</div>
                            <code className="block rounded-md bg-(--secondary-w10) p-2 text-xs break-all">{data.metadata.condition}</code>
                        </div>
                    )}
                    {data.errorMessage && (
                        <div className="text-xs text-(--error-w100)">
                            <span className="font-semibold">Error:</span> {data.errorMessage}
                        </div>
                    )}
                    {data.toolResultData !== undefined && (
                        <div>
                            <div className="mb-2 text-xs font-semibold">Result:</div>
                            {renderResultValue(data.toolResultData)}
                        </div>
                    )}
                </div>
            </div>
        );
    };

    const renderWorkflowStart = (step: VisualizerStep) => {
        const data = step.data.workflowExecutionStart;
        if (!data) return null;

        return (
            <div>
                <h4 className="mb-2 text-sm font-semibold">Workflow Start</h4>
                <div className="space-y-2">
                    <div className="text-xs">
                        <span className="font-semibold">Workflow:</span> {data.workflowName}
                    </div>
                    {data.workflowInput && (
                        <div>
                            <div className="mb-2 text-xs font-semibold">Input:</div>
                            {renderFormattedArguments(data.workflowInput)}
                        </div>
                    )}
                </div>
            </div>
        );
    };

    const renderWorkflowResult = (step: VisualizerStep) => {
        const data = step.data.workflowExecutionResult;
        if (!data) return null;

        return (
            <div>
                <h4 className="mb-2 text-sm font-semibold">Workflow Result</h4>
                <div className="space-y-2">
                    <div className="text-xs">
                        <span className="font-semibold">Status:</span> <span className={data.status === "success" ? "text-(--success-wMain)" : "text-(--error-w100)"}>{data.status}</span>
                    </div>
                    {data.workflowOutput && (
                        <div>
                            <div className="mb-2 text-xs font-semibold">Output:</div>
                            {renderFormattedArguments(data.workflowOutput)}
                        </div>
                    )}
                    {data.errorMessage && (
                        <div className="text-xs text-(--error-w100)">
                            <span className="font-semibold">Error:</span> {data.errorMessage}
                        </div>
                    )}
                </div>
            </div>
        );
    };

    const hasRequestAndResult = nodeDetails.requestStep && nodeDetails.resultStep;
    const hasCreatedArtifacts = nodeDetails.createdArtifacts && nodeDetails.createdArtifacts.length > 0;

    const renderOutputArtifact = () => {
        const outputArtifactRef = nodeDetails.outputArtifactStep?.data?.workflowNodeExecutionResult?.outputArtifactRef;
        if (!outputArtifactRef) return null;

        return (
            <div className="mt-4 border-t pt-4">
                <div className="mb-2 flex min-w-0 items-baseline gap-1 text-xs font-semibold">
                    <span className="flex-shrink-0">Output Artifact:</span>
                    <span className="min-w-0 truncate font-normal text-(--secondary-text-wMain)" title={outputArtifactRef.name}>
                        {outputArtifactRef.name}
                    </span>
                    {outputArtifactRef.version !== undefined && <span className={cn("ml-1 shrink-0", NODE_ACCENT_COLOR)}>v{outputArtifactRef.version}</span>}
                </div>
                <div className="rounded-md border p-2">
                    <ArtifactContentViewer name={outputArtifactRef.name} version={outputArtifactRef.version} />
                </div>
            </div>
        );
    };

    const renderCreatedArtifacts = (asColumn: boolean = false) => {
        if (!nodeDetails.createdArtifacts || nodeDetails.createdArtifacts.length === 0) return null;

        const handleArtifactClick = (filename: string, version?: number) => {
            const artifact = artifacts.find(a => a.filename === filename);

            if (artifact) {
                setActiveSidePanelTab("files");

                setIsSidePanelCollapsed(false);

                setSidePanelPreviewArtifact(artifact);

                if (version !== undefined && version !== artifact.version) {
                    setTimeout(() => {
                        navigateArtifactVersion(filename, version, overrideSessionId);
                    }, 100);
                }

                onClose?.();
            }
        };

        return (
            <div className={asColumn ? "" : "mt-4 border-t pt-4"}>
                <div className={`flex items-center gap-2 ${asColumn ? "mb-3 border-b pb-2" : "mb-3"}`}>
                    <div className={cn(asColumn && "h-2 w-2 rounded-full bg-(--accent-n1-wMain)")}></div>
                    <FileText className={cn("h-4 w-4", NODE_COLORS.artifact, asColumn && "hidden")} />
                    <h4 className={cn("text-sm font-bold", ARTIFACT_CARD_STYLES.text)}>{asColumn ? "CREATED ARTIFACTS" : `Created Artifacts (${nodeDetails.createdArtifacts.length})`}</h4>
                </div>
                <div className="space-y-3">
                    {nodeDetails.createdArtifacts.map((artifact, index) => (
                        <div key={`${artifact.filename}-${artifact.version ?? index}`} className={cn("rounded-md border p-3", ARTIFACT_CARD_STYLES.border, ARTIFACT_CARD_STYLES.bg)}>
                            <div className="mb-1 flex items-center justify-between gap-2">
                                <button
                                    onClick={() => handleArtifactClick(artifact.filename, artifact.version)}
                                    className={cn("min-w-0 cursor-pointer truncate text-sm font-semibold transition-colors", ARTIFACT_CARD_STYLES.text, ARTIFACT_CARD_STYLES.textHover)}
                                    title={artifact.filename}
                                >
                                    {artifact.filename}
                                </button>
                                <div className="flex flex-shrink-0 items-center">
                                    {artifact.version !== undefined && <span className={cn("rounded px-1.5 py-0.5 text-xs", ARTIFACT_CARD_STYLES.badgeBg, ARTIFACT_CARD_STYLES.text)}>v{artifact.version}</span>}
                                </div>
                            </div>
                            {artifact.description && <p className="mb-2 text-xs">{artifact.description}</p>}
                            {artifact.mimeType && (
                                <div className="text-xs text-(--secondary-text-wMain)">
                                    <span className="font-medium">Type:</span> {artifact.mimeType}
                                </div>
                            )}
                            <div className="mt-2 rounded-lg bg-(--background-w10) p-2">
                                <ArtifactContentViewer name={artifact.filename} version={artifact.version} mimeType={artifact.mimeType} />
                            </div>
                        </div>
                    ))}
                </div>
            </div>
        );
    };

    const renderMainContent = () => (
        <div className="flex flex-col">
            <div className="flex flex-shrink-0 items-center gap-3 border-b p-4">
                {getNodeIcon()}
                <div className="min-w-0 flex-1">
                    <h3 className="truncate text-base font-bold">{nodeDetails.label}</h3>
                    {nodeDetails.description ? (
                        <p className="truncate text-xs text-(--secondary-text-wMain)" title={nodeDetails.description}>
                            {nodeDetails.description}
                        </p>
                    ) : (
                        <p className="text-xs text-(--secondary-text-wMain) capitalize">{nodeDetails.nodeType} Node</p>
                    )}
                </div>
            </div>

            <div className="min-h-0 flex-1 overflow-hidden">
                {hasRequestAndResult ? (
                    <div className={`grid h-full grid-cols-1 ${hasCreatedArtifacts ? "lg:grid-cols-3" : "lg:grid-cols-2"} divide-y divide-(--secondary-w40) lg:divide-x lg:divide-y-0`}>
                        <div className="max-h-[calc(85vh-140px)] overflow-y-auto p-4">
                            <ColumnHeader label="REQUEST" color="(--info-wMain)" />
                            {renderStepContent(nodeDetails.requestStep, true)}
                        </div>

                        <div className="max-h-[calc(85vh-140px)] overflow-y-auto p-4">
                            <ColumnHeader label="RESULT" color="(--success-wMain)" />
                            {renderStepContent(nodeDetails.resultStep, false)}
                            {renderOutputArtifact()}
                        </div>

                        {hasCreatedArtifacts && <div className="max-h-[calc(85vh-140px)] overflow-y-auto p-4">{renderCreatedArtifacts(true)}</div>}
                    </div>
                ) : (
                    <div className="max-h-[calc(85vh-140px)] overflow-y-auto p-4">
                        {nodeDetails.requestStep && (
                            <div className="mb-4">
                                <div className="mb-3 flex items-center gap-2 border-b pb-2">
                                    <div className="h-2 w-2 rounded-full bg-(--info-wMain)"></div>
                                    <h4 className="text-sm font-bold text-(--info-wMain)">REQUEST</h4>
                                </div>
                                {renderStepContent(nodeDetails.requestStep, true)}
                            </div>
                        )}
                        {nodeDetails.resultStep && (
                            <div>
                                <div className="mb-3 flex items-center gap-2 border-b pb-2">
                                    <div className="h-2 w-2 rounded-full bg-(--success-wMain)"></div>
                                    <h4 className="text-sm font-bold text-(--success-wMain)">RESULT</h4>
                                </div>
                                {renderStepContent(nodeDetails.resultStep, false)}
                                {renderOutputArtifact()}
                                {renderCreatedArtifacts()}
                            </div>
                        )}
                        {!nodeDetails.requestStep && !nodeDetails.resultStep && <div className="flex h-32 items-center justify-center text-(--secondary-text-wMain) italic">No detailed information available for this node</div>}
                    </div>
                )}
            </div>
        </div>
    );

    return renderMainContent();
};

export default NodeDetailsCard;
