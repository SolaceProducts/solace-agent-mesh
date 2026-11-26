// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useMemo, useState } from "react";
import { useBooleanFlagDetails } from "@openfeature/react-sdk";
import DOMPurify from "dompurify";
import { Quote, ThumbsDown, ThumbsUp, Workflow } from "lucide-react";
import type { ReactNode } from "react";

import type { BuilderContextValue, ChatMessageData } from "@/providers/contexts";
import { useBuilderContext, useChatMessageData } from "@/providers/contexts";

import { InlineNotice } from "@/common/banners/InlineNotice";
import { MessageBanner } from "@/common/banners/MessageBanner";
import { MarkdownHTMLConverter, MarkdownWrapper } from "@/common/markdown";

import { parseCitations } from "@/utils/citations";
import { isDeepResearchReportFilename } from "@/utils/deepResearchUtils";
import { downloadFile } from "@/utils/download";
import { aggregateRagSources, hasDocumentSearchResults } from "@/utils/sourceUrlHelpers";

import { ChatBubble, ChatBubbleMessage } from "@/ui/chat/chat-bubble";

import { FreeModelErrorActions } from "@/features/activation";
import { BUILD_MANIFEST_ARTIFACT_FILENAME, PLATFORM_COMPONENTS_ARTIFACT_FILENAME } from "@/features/builder/builderArtifacts";
import { isFileShownAsCard, isHiddenWorkingPart } from "@/features/chat/artifactTags";
import { useCitationClick } from "@/features/chat/hooks";
import { ImageSearchGrid } from "@/features/chat/research";
import { DeepResearchReportContent } from "@/features/chat/research/DeepResearchReportContent";
import { InlineResearchProgress, type ResearchProgressData } from "@/features/chat/research/InlineResearchProgress";
import { type ResearchPlanData, ResearchPlanVerification } from "@/features/chat/research/ResearchPlanVerification";
import { Sources } from "@/features/chat/sources/Sources";

import type { ArtifactInfo, ArtifactPart, DataPart, FileAttachment, FilePart, MessageFE, PartFE, RAGSearchResult, TextPart } from "@/types";
import { Button } from "@/ui";

import { UserInputMessage } from "./a2ui/UserInputMessage";
import { BuilderProgressCard } from "./artifact/BuilderProgressCard";
import { BuildPlanCard } from "./artifact/BuildPlanCard";
import { AuthenticationMessage } from "./authentication/AuthenticationMessage";
import { CompactionNotification, type CompactionNotificationData } from "./CompactionNotification";
import { FeedbackModal } from "./FeedbackModal";
import { ArtifactMessage } from "./file/ArtifactMessage";
import { FileMessage } from "./file/FileMessage";
import { InlineProgressUpdates } from "./InlineProgressUpdates";
import { MessageAttribution } from "./MessageAttribution";
import { MessageHoverButtons } from "./MessageHoverButtons";
import { ContentRenderer } from "./preview/ContentRenderer";
import type { ExtractedContent } from "./preview/contentUtils";
import { extractEmbeddedContent } from "./preview/contentUtils";
import { decodeBase64Content } from "./preview/previewUtils";
import { RedirectButton, type RedirectData } from "./RedirectButton";
import { SelectableMessageContent } from "./selection";

interface BuildPlanPart {
    kind: "build-plan";
    filename: string;
    isComplete: boolean;
}

function isOtherUserMessage(message: MessageFE, currentUserEmail: string): boolean {
    if (!message.isUser) return false;
    if (!message.senderEmail) return false;
    if (currentUserEmail) {
        return message.senderEmail.toLowerCase() !== currentUserEmail.toLowerCase();
    }
    return false;
}

function getUserIndexFromEmail(email: string): number {
    if (!email) return 0;
    return email
        .toLowerCase()
        .split("")
        .reduce((acc, char) => acc + char.charCodeAt(0), 0);
}

const RENDER_TYPES_WITH_RAW_CONTENT = ["image", "audio"];

const MessageActions: React.FC<{
    message: MessageFE;
    showFeedbackActions: boolean;
    sourcesElement?: React.ReactNode;
    textContentOverride?: string;
}> = ({ message, showFeedbackActions, sourcesElement, textContentOverride }) => {
    const { configCollectFeedback, submittedFeedback, handleFeedbackSubmit, addNotification } = useChatMessageData();

    const [isFeedbackModalOpen, setIsFeedbackModalOpen] = useState(false);
    const [feedbackType, setFeedbackType] = useState<"up" | "down" | null>(null);
    const [feedbackAlreadyRecorded, setFeedbackAlreadyRecorded] = useState(false);

    const taskId = message.taskId;
    const submittedFeedbackType = taskId ? submittedFeedback[taskId]?.type : undefined;
    const feedbackLocked = !!submittedFeedbackType || feedbackAlreadyRecorded;

    const handleThumbClick = (type: "up" | "down") => {
        setFeedbackType(type);
        setIsFeedbackModalOpen(true);
    };

    const handleModalClose = () => {
        setIsFeedbackModalOpen(false);
        setFeedbackType(null);
    };

    const handleModalSubmit = async (feedbackText: string) => {
        if (!feedbackType || !taskId) return;

        try {
            await handleFeedbackSubmit(taskId, feedbackType, feedbackText);
            addNotification("Feedback submitted successfully", "success");
        } catch (error) {
            if ((error as { status?: number }).status === 409) {
                setFeedbackAlreadyRecorded(true);
                addNotification("Feedback for this response was already recorded.", "info");
                return;
            }
            throw error;
        }
    };

    const shouldShowFeedback = showFeedbackActions && configCollectFeedback;

    if (!showFeedbackActions && !sourcesElement) {
        return null;
    }

    return (
        <>
            <div className="mt-3 space-y-2">
                <div className="flex items-center justify-start">
                    {shouldShowFeedback && (
                        <div className="flex items-center">
                            <Button tooltip="Like" variant="ghost" size="sm" className={`${submittedFeedbackType ? "!opacity-100" : ""}`} onClick={() => handleThumbClick("up")} disabled={feedbackLocked}>
                                <ThumbsUp className={`h-4 w-4 ${submittedFeedbackType === "up" ? "fill-[var(--brand-wMain)] text-[var(--brand-wMain)] !opacity-100" : ""}`} />
                            </Button>
                            <Button tooltip="Dislike" variant="ghost" size="sm" className={`${submittedFeedbackType ? "!opacity-100" : ""}`} onClick={() => handleThumbClick("down")} disabled={feedbackLocked}>
                                <ThumbsDown className={`h-4 w-4 ${submittedFeedbackType === "down" ? "fill-[var(--brand-wMain)] text-[var(--brand-wMain)] opacity-100" : ""}`} />
                            </Button>
                            <output className="text-muted-foreground ml-1 text-xs">{feedbackAlreadyRecorded && !submittedFeedbackType ? "Feedback already recorded" : ""}</output>
                        </div>
                    )}
                    <MessageHoverButtons message={message} textContentOverride={textContentOverride} />
                    {sourcesElement && <div className="ml-2">{sourcesElement}</div>}
                </div>
            </div>
            {feedbackType && <FeedbackModal isOpen={isFeedbackModalOpen} onClose={handleModalClose} feedbackType={feedbackType} onSubmit={handleModalSubmit} />}
        </>
    );
};

const getUserFriendlyErrorMessage = (technicalMessage: string): string => {
    if (technicalMessage.includes("No FilePart found in message for structured schema")) {
        return "This workflow requires a file to be uploaded. Please attach a file and try again.";
    }

    if (technicalMessage.includes("Workflow failed:") && technicalMessage.includes("Node execution error:")) {
        const match = technicalMessage.match(/Node execution error:\s*(.+)$/);
        if (match) {
            return `The workflow encountered an error: ${match[1]}`;
        }
    }

    if (technicalMessage.startsWith("Workflow failed:")) {
        return technicalMessage.replace(/^Workflow failed:\s*/, "The workflow encountered an error: ");
    }

    if (technicalMessage.toLowerCase().includes("task failed")) {
        return "The request could not be completed. Please try again or contact support.";
    }

    return technicalMessage;
};

const MessageContent = React.memo<{ message: MessageFE; isStreaming?: boolean; highlightedText?: string | null }>(({ message, isStreaming, highlightedText }) => {
    const [renderError, setRenderError] = useState<string | null>(null);
    const { sessionId, ragData } = useChatMessageData();
    const handleCitationClick = useCitationClick(message.taskId);
    const contentRef = React.useRef<HTMLDivElement>(null);

    React.useEffect(() => {
        if (!highlightedText || !contentRef.current) return;

        const highlightStyle = "background-color: color-mix(in srgb, var(--brand-wMain) 30%, transparent); color: inherit; transition: background-color 0.5s ease-out;";
        const highlightMarks: HTMLElement[] = [];

        const normalizeText = (text: string) => text.replace(/\s+/g, " ").trim();
        const searchTextNormalized = normalizeText(highlightedText);

        const collectTextNodes = (container: HTMLElement): { node: Text; start: number; end: number }[] => {
            const textNodes: { node: Text; start: number; end: number }[] = [];
            const walker = document.createTreeWalker(container, NodeFilter.SHOW_TEXT, null);
            let node: Text | null;
            let position = 0;

            while ((node = walker.nextNode() as Text | null)) {
                const text = node.textContent || "";
                if (text.length > 0) {
                    textNodes.push({
                        node,
                        start: position,
                        end: position + text.length,
                    });
                    position += text.length;
                }
            }
            return textNodes;
        };

        const findTextInNodes = (textNodes: { node: Text; start: number; end: number }[], searchText: string): { found: boolean; nodeRanges: { node: Text; startOffset: number; endOffset: number }[] } => {
            const fullText = textNodes.map(t => t.node.textContent || "").join("");

            const normalizedFullText = normalizeText(fullText);
            const normalizedSearchText = normalizeText(searchText);

            const normalizedIndex = normalizedFullText.indexOf(normalizedSearchText);

            if (normalizedIndex === -1) {
                const lowerNormalizedIndex = normalizedFullText.toLowerCase().indexOf(normalizedSearchText.toLowerCase());
                if (lowerNormalizedIndex === -1) {
                    return { found: false, nodeRanges: [] };
                }
            }

            const normalizedToOriginal: number[] = [];
            let lastWasSpace = false;

            for (let i = 0; i < fullText.length; i++) {
                const char = fullText[i];
                const isSpace = /\s/.test(char);

                if (isSpace) {
                    if (!lastWasSpace) {
                        normalizedToOriginal.push(i);
                    }
                    lastWasSpace = true;
                } else {
                    normalizedToOriginal.push(i);
                    lastWasSpace = false;
                }
            }

            let leadingSpaceCount = 0;
            for (const char of fullText) {
                if (/\s/.test(char)) leadingSpaceCount++;
                else break;
            }

            const trimmedMapping = normalizedToOriginal.filter(pos => pos >= leadingSpaceCount);

            let actualNormalizedIndex = normalizedFullText.indexOf(normalizedSearchText);
            if (actualNormalizedIndex === -1) {
                actualNormalizedIndex = normalizedFullText.toLowerCase().indexOf(normalizedSearchText.toLowerCase());
            }

            if (actualNormalizedIndex === -1 || actualNormalizedIndex >= trimmedMapping.length) {
                return { found: false, nodeRanges: [] };
            }

            const searchEndIndex = Math.min(actualNormalizedIndex + normalizedSearchText.length - 1, trimmedMapping.length - 1);

            if (searchEndIndex < 0 || actualNormalizedIndex < 0) {
                return { found: false, nodeRanges: [] };
            }

            const originalStart = trimmedMapping[actualNormalizedIndex];
            const originalEnd = trimmedMapping[searchEndIndex] + 1;

            const nodeRanges: { node: Text; startOffset: number; endOffset: number }[] = [];
            let currentPos = 0;

            for (const { node } of textNodes) {
                const text = node.textContent || "";
                const nodeStart = currentPos;
                const nodeEnd = currentPos + text.length;

                if (nodeEnd > originalStart && nodeStart < originalEnd) {
                    const startOffset = Math.max(0, originalStart - nodeStart);
                    const endOffset = Math.min(text.length, originalEnd - nodeStart);

                    if (startOffset < endOffset) {
                        nodeRanges.push({ node, startOffset, endOffset });
                    }
                }

                currentPos = nodeEnd;
            }

            return { found: nodeRanges.length > 0, nodeRanges };
        };

        const highlightNodeRanges = (nodeRanges: { node: Text; startOffset: number; endOffset: number }[]): HTMLElement[] => {
            const marks: HTMLElement[] = [];

            for (const { node, startOffset, endOffset } of nodeRanges) {
                const text = node.textContent || "";
                const parent = node.parentNode;
                if (!parent) continue;

                const highlightText = text.slice(startOffset, endOffset);

                if (/^\s*$/.test(highlightText)) {
                    continue;
                }

                const frag = document.createDocumentFragment();

                if (startOffset > 0) {
                    frag.appendChild(document.createTextNode(text.slice(0, startOffset)));
                }

                const mark = document.createElement("mark");
                mark.style.cssText = highlightStyle;
                mark.textContent = highlightText;
                frag.appendChild(mark);
                marks.push(mark);

                if (endOffset < text.length) {
                    frag.appendChild(document.createTextNode(text.slice(endOffset)));
                }

                parent.replaceChild(frag, node);
            }

            return marks;
        };

        const textNodes = collectTextNodes(contentRef.current);

        let result = findTextInNodes(textNodes, highlightedText);

        if (!result.found) {
            const words = searchTextNormalized.split(/\s+/);
            for (const wordCount of [Math.min(5, words.length), 4, 3, 2, 1]) {
                if (result.found) break;
                if (words.length < wordCount) continue;

                const searchText = words.slice(0, wordCount).join(" ");
                if (searchText.length < 3) continue;

                const freshTextNodes = collectTextNodes(contentRef.current!);
                result = findTextInNodes(freshTextNodes, searchText);
            }
        }

        if (!result.found && searchTextNormalized.length > 0) {
            const words = searchTextNormalized.split(/\s+/).filter(w => w.length >= 2);
            if (words.length > 0) {
                const freshTextNodes = collectTextNodes(contentRef.current!);
                const fullText = freshTextNodes.map(t => t.node.textContent || "").join("");
                const normalizedFullText = normalizeText(fullText).toLowerCase();

                for (let startWord = 0; startWord < words.length && !result.found; startWord++) {
                    for (let endWord = words.length; endWord > startWord && !result.found; endWord--) {
                        const searchPhrase = words.slice(startWord, endWord).join(" ").toLowerCase();
                        if (normalizedFullText.includes(searchPhrase)) {
                            const latestTextNodes = collectTextNodes(contentRef.current!);
                            result = findTextInNodes(latestTextNodes, words.slice(startWord, endWord).join(" "));
                            break;
                        }
                    }
                }
            }
        }

        if (result.found && result.nodeRanges.length > 0) {
            const marks = highlightNodeRanges(result.nodeRanges);
            highlightMarks.push(...marks);
        }

        if (highlightMarks.length > 0) {
            highlightMarks[0].scrollIntoView({ behavior: "smooth", block: "center" });

            setTimeout(() => {
                highlightMarks.forEach(mark => {
                    mark.style.backgroundColor = "transparent";
                });
            }, 3000);

            setTimeout(() => {
                highlightMarks.forEach(mark => {
                    if (mark.parentNode) {
                        const textNode = document.createTextNode(mark.textContent || "");
                        mark.parentNode.replaceChild(textNode, mark);
                    }
                });
            }, 3500);
        }
    }, [highlightedText]);

    let textContent =
        message.parts
            ?.filter(p => p.kind === "text")
            .map(p => (p as TextPart).text)
            .join("") || "";

    if (message.isUser && message.contextQuote) {
        const contextPrefixRegex = /^Context:\s*"[^"]*"\s*\n\n/;
        textContent = textContent.replace(contextPrefixRegex, "");
    }

    const displayText = message.isUser ? textContent.trim() : textContent;

    const taskRagData = useMemo(() => {
        if (!message.taskId || !ragData) return undefined;
        return aggregateRagSources(ragData.filter(r => r.taskId === message.taskId));
    }, [message.taskId, ragData]);

    const citations = useMemo(() => {
        if (message.isUser) return [];
        return parseCitations(displayText, taskRagData);
    }, [displayText, taskRagData, message.isUser]);

    const embeddedContent = useMemo(() => extractEmbeddedContent(displayText), [displayText]);

    const { modifiedText, contentElements } = useMemo(() => {
        if (embeddedContent.length === 0) {
            return { modifiedText: displayText, contentElements: [] };
        }

        let modText = displayText;
        const elements: ReactNode[] = [];
        embeddedContent.forEach((item: ExtractedContent, index: number) => {
            modText = modText.replace(item.originalMatch, "");

            if (item.type === "file") {
                const fileAttachment: FileAttachment = {
                    name: item.filename || "downloaded_file",
                    content: item.content,
                    mimeType: item.mimeType,
                };
                elements.push(
                    <div key={`embedded-file-${index}`} className="my-2">
                        <FileMessage filename={fileAttachment.name} mimeType={fileAttachment.mimeType} onDownload={() => downloadFile(fileAttachment, sessionId)} isEmbedded={true} />
                    </div>
                );
            } else if (!RENDER_TYPES_WITH_RAW_CONTENT.includes(item.type)) {
                const finalContent = decodeBase64Content(item.content);
                if (finalContent) {
                    elements.push(
                        <div key={`embedded-${item.originalMatch}`} className="my-2 h-auto w-full max-w-md">
                            <ContentRenderer content={finalContent} rendererType={item.type} mimeType={item.mimeType} setRenderError={setRenderError} ragData={taskRagData} />
                        </div>
                    );
                }
            }
        });

        return { modifiedText: modText, contentElements: elements };
    }, [embeddedContent, displayText, sessionId, setRenderError, taskRagData]);

    const modifiedCitations = useMemo(() => {
        if (message.isUser) return [];
        return parseCitations(modifiedText, taskRagData);
    }, [modifiedText, taskRagData, message.isUser]);

    if (message.isUser && message.metadata?.hidden === true) {
        return null;
    }

    const chip = message.isUser ? message.metadata?.chip : undefined;
    if (chip && typeof chip === "object" && "label" in chip && !(chip as { kind?: string }).kind) {
        const { emoji, label } = chip as { emoji?: string; label: string };
        return (
            <span className="inline-flex items-center gap-1 rounded-md px-3 py-1 text-[13px] font-medium opacity-70">
                {emoji ? <span aria-hidden="true">{emoji}</span> : null}
                <span>{label}</span>
            </span>
        );
    }

    if (message.isUser && message.displayHtml) {
        const parser = new DOMParser();
        const doc = parser.parseFromString(message.displayHtml, "text/html");

        const quoteBadges = doc.querySelectorAll(".context-quote-badge");
        quoteBadges.forEach(badge => badge.remove());

        const htmlToRender = doc.body.innerHTML;

        const cleanHtml = DOMPurify.sanitize(htmlToRender, {
            ALLOWED_TAGS: ["span", "br", "div", "svg", "path"],
            ALLOWED_ATTR: ["class", "contenteditable", "data-internal", "data-person-id", "data-person-name", "data-display", "xmlns", "width", "height", "viewBox", "fill", "stroke", "stroke-width", "stroke-linecap", "stroke-linejoin", "d"],
        });

        return <div className="message-with-mentions break-words whitespace-pre-wrap" dangerouslySetInnerHTML={{ __html: cleanHtml }} />;
    }

    const renderContent = () => {
        if (message.isWarning) {
            return (
                <InlineNotice variant="warning">
                    <MarkdownHTMLConverter>{displayText}</MarkdownHTMLConverter>
                </InlineNotice>
            );
        }

        if (message.isError) {
            const friendlyErrorMessage = getUserFriendlyErrorMessage(displayText);
            return (
                <>
                    <InlineNotice variant="error">
                        <MarkdownHTMLConverter>{friendlyErrorMessage}</MarkdownHTMLConverter>
                    </InlineNotice>
                    <FreeModelErrorActions message={displayText} />
                </>
            );
        }

        if (embeddedContent.length === 0) {
            return <MarkdownWrapper content={displayText} isStreaming={isStreaming} citations={citations} onCitationClick={handleCitationClick} />;
        }

        return (
            <div>
                {renderError && <MessageBanner variant="error" message="Error rendering preview" />}
                <MarkdownWrapper content={modifiedText} isStreaming={isStreaming} citations={modifiedCitations} onCitationClick={handleCitationClick} />
                {contentElements}
            </div>
        );
    };

    if (!message.isUser) {
        return (
            <div ref={contentRef}>
                <SelectableMessageContent messageId={message.metadata?.messageId || ""} taskId={message.taskId} isAIMessage={true}>
                    {renderContent()}
                </SelectableMessageContent>
            </div>
        );
    }

    return <div ref={contentRef}>{renderContent()}</div>;
});

const MessageWrapper = React.memo<{ message: MessageFE; children: ReactNode; className?: string }>(({ message, children, className }) => {
    return <div className={`mt-1 space-y-1 ${message.isUser ? "ml-auto" : "mr-auto"} ${className}`}>{children}</div>;
});

const getUploadedFiles = (message: MessageFE, alignRight: boolean = true) => {
    const hasUploads = !!message.uploadedFiles && message.uploadedFiles.length > 0;
    const hasAttached = !!message.attachedArtifacts && message.attachedArtifacts.length > 0;
    if (!hasUploads && !hasAttached) return null;

    return (
        <MessageWrapper message={message} className={`flex flex-wrap gap-2 ${alignRight ? "justify-end" : "justify-start"}`}>
            {message.uploadedFiles?.map((file, fileIdx) => (
                <FileMessage key={`uploaded-${message.metadata?.messageId}-${fileIdx}`} filename={file.name} mimeType={file.type} />
            ))}
            {message.attachedArtifacts?.map((ref, refIdx) => (
                <FileMessage key={`attached-${message.metadata?.messageId}-${refIdx}`} filename={ref.filename} mimeType={ref.mimeType} />
            ))}
        </MessageWrapper>
    );
};

interface DeepResearchReportInfo {
    artifact: ArtifactInfo;
    sessionId: string;
    ragData?: RAGSearchResult;
}

const DeepResearchReportBubble: React.FC<{
    deepResearchReportInfo: DeepResearchReportInfo;
    message: MessageFE;
    onContentLoaded?: (content: string) => void;
}> = ({ deepResearchReportInfo, message, onContentLoaded }) => {
    return (
        <ChatBubble variant="received">
            <ChatBubbleMessage variant="received">
                <SelectableMessageContent messageId={message.metadata?.messageId || ""} taskId={message.taskId} isAIMessage={true}>
                    <DeepResearchReportContent artifact={deepResearchReportInfo.artifact} sessionId={deepResearchReportInfo.sessionId} ragData={deepResearchReportInfo.ragData} onContentLoaded={onContentLoaded} />
                </SelectableMessageContent>
            </ChatBubbleMessage>
        </ChatBubble>
    );
};

function shouldHideWorkingCard(part: PartFE, showWorkingArtifacts: boolean): boolean {
    return !showWorkingArtifacts && isHiddenWorkingPart(part);
}

function shouldHideFileCard(part: PartFE, showWorkingArtifacts: boolean, parts: PartFE[]): boolean {
    return shouldHideWorkingCard(part, showWorkingArtifacts) || isFileShownAsCard(part, parts);
}

function isTestHarnessArtifact(part: PartFE): boolean {
    if (part.kind !== "artifact") {
        return false;
    }
    const name = (part as ArtifactPart).name || "";
    return name === "test_plan.json" || name === "test_report.json";
}

function classifyBuilderArtifact(artifactPart: ArtifactPart, allArtifacts: ArtifactInfo[], artifacts: ArtifactInfo[]): { isBuildManifest: boolean; hide: boolean } {
    const name = artifactPart.name || "";
    const mimeType = artifactPart.file?.mimeType || allArtifacts?.find(a => a.filename === name)?.mimeType || artifacts.find(a => a.filename === name)?.mimeType || "";
    const isSamConfig = mimeType.startsWith("application/vnd.sam-");
    const isBuildManifest = mimeType === "application/vnd.sam-build-manifest+yaml" || name === BUILD_MANIFEST_ARTIFACT_FILENAME;
    const isInProgress = artifactPart.status === "in-progress";
    return { isBuildManifest, hide: name === PLATFORM_COMPONENTS_ARTIFACT_FILENAME || isSamConfig || isBuildManifest || isInProgress };
}

const getChatBubble = (
    message: MessageFE,
    chatContext: ChatMessageData,
    artifacts: ArtifactInfo[],
    allArtifacts: ArtifactInfo[],
    builderContext: BuilderContextValue,
    isLastWithTaskId?: boolean,
    isStreaming?: boolean,
    sourcesElement?: React.ReactNode,
    deepResearchReportInfo?: DeepResearchReportInfo,
    onReportContentLoaded?: (content: string) => void,
    reportContentOverride?: string,
    highlightedText?: string | null,
    showThinkingContentEnabled?: boolean,
    hideInlineActivityTimeline?: boolean
): React.ReactNode => {
    const { openSidePanelTab, setTaskIdInSidePanel, ragData, currentUserEmail } = chatContext;
    const { builderMode, builderCreationState } = builderContext;

    if (message.isStatusBubble) {
        return null;
    }

    if (message.authenticationLink) {
        return <AuthenticationMessage message={message} />;
    }

    const shouldRenderUserInput = message.userInputRequest && !(builderMode && !message.userInputRequest.responded && !message.userInputRequest.timedOut && !message.userInputRequest.cancelled);

    const planPart = message.parts?.find(p => p.kind === "data" && (p as DataPart).data && ((p as DataPart).data as { type?: string }).type === "deep_research_plan") as DataPart | undefined;
    if (planPart?.data) {
        const planData = planPart.data as unknown as ResearchPlanData;
        return <ResearchPlanVerification planData={planData} />;
    }

    const progressPart = message.parts?.find(p => p.kind === "data") as DataPart | undefined;
    const hasDeepResearchProgress = progressPart?.data && (progressPart.data as { type?: string }).type === "deep_research_progress";

    if (hasDeepResearchProgress && !message.isComplete) {
        const data = progressPart!.data as unknown as ResearchProgressData;
        const taskRagData = ragData?.filter(r => r.taskId === message.taskId);
        const hasOtherContent = message.parts?.some(p => (p.kind === "text" && (p as TextPart).text.trim()) || p.kind === "artifact" || p.kind === "file");

        const progressBlock = (
            <div className="my-2">
                <InlineResearchProgress progress={data} isComplete={false} ragData={taskRagData} />
            </div>
        );

        if (!hasOtherContent) {
            return progressBlock;
        }

        const messageWithoutProgress = {
            ...message,
            parts: message.parts?.filter(p => p.kind !== "data"),
        };

        return (
            <>
                {progressBlock}
                {getChatBubble(messageWithoutProgress, chatContext, artifacts, allArtifacts, builderContext, isLastWithTaskId, undefined, undefined, undefined, undefined, undefined, undefined, showThinkingContentEnabled)}
            </>
        );
    }

    const compactionPart = message.parts?.find(p => p.kind === "data" && (p as DataPart).data?.type === "compaction_notification") as DataPart | undefined;
    if (compactionPart) {
        return <CompactionNotification data={compactionPart.data as unknown as CompactionNotificationData} />;
    }

    const redirectPart = message.parts?.find(p => p.kind === "data" && (p as DataPart).data && (p as DataPart).data.type === "redirect") as DataPart | undefined;

    const groupedParts: (TextPart | FilePart | ArtifactPart | BuildPlanPart)[] = [];
    let currentTextGroup = "";
    const flushTextGroup = () => {
        if (currentTextGroup) {
            groupedParts.push({ kind: "text", text: currentTextGroup });
            currentTextGroup = "";
        }
    };
    message.parts?.forEach(part => {
        if (part.kind === "text") {
            currentTextGroup += (part as TextPart).text;
        } else if (part.kind === "file" || part.kind === "artifact") {
            if (isTestHarnessArtifact(part)) {
                return;
            }
            if (builderMode && part.kind === "artifact") {
                const artifactPart = part as ArtifactPart;
                const { isBuildManifest, hide } = classifyBuilderArtifact(artifactPart, allArtifacts, artifacts);
                if (isBuildManifest) {
                    flushTextGroup();
                    groupedParts.push({ kind: "build-plan", filename: artifactPart.name || "", isComplete: artifactPart.status === "completed" });
                }
                if (hide) {
                    return;
                }
            }
            if (shouldHideFileCard(part, chatContext.showWorkingArtifacts, message.parts)) {
                return;
            }
            flushTextGroup();
            groupedParts.push(part);
        }
    });
    if (currentTextGroup) {
        groupedParts.push({ kind: "text", text: currentTextGroup });
    }

    const hasContent = groupedParts.some(p => (p.kind === "text" && (p as TextPart).text.trim()) || p.kind === "file" || p.kind === "artifact" || p.kind === "build-plan");

    if (shouldRenderUserInput && !hasContent) {
        return <UserInputMessage message={message} />;
    }
    if (message.userInputRequest && !shouldRenderUserInput && !hasContent) {
        return null;
    }

    const hasProgressUpdates = message.progressUpdates && message.progressUpdates.length > 0;
    if (!hasContent && !hasProgressUpdates) {
        return null;
    }

    const isOtherUser = message.isUser && isOtherUserMessage(message, currentUserEmail);
    const variant = message.isUser && !isOtherUser ? "sent" : isOtherUser ? "other-user" : "received";
    const isRightAligned = message.isUser && !isOtherUser;
    const showFeedbackActions = !message.isUser && message.isComplete && !!message.taskId && !!isLastWithTaskId;

    const handleViewWorkflowClick = () => {
        if (message.taskId) {
            setTaskIdInSidePanel(message.taskId);
            openSidePanelTab("activity");
        }
    };

    const renderTextPart = (part: TextPart, index: number, isLastPart: boolean, shouldStream?: boolean) => {
        const textContent = part.text;
        if (!textContent?.trim()) {
            if (!isLastPart || !showFeedbackActions) return null;
            return (
                <div key={`part-${index}`} className={`flex ${isRightAligned ? "justify-end pr-4" : "justify-start pl-4"}`}>
                    <MessageActions message={message} showFeedbackActions={!!showFeedbackActions} textContentOverride={reportContentOverride} />
                </div>
            );
        }

        return (
            <ChatBubble key={`part-${index}`} variant={variant}>
                <ChatBubbleMessage variant={variant}>
                    <MessageContent message={{ ...message, parts: [{ kind: "text", text: textContent }] }} isStreaming={shouldStream} highlightedText={highlightedText} />
                    {isLastPart && redirectPart && message.isComplete && <RedirectButton data={redirectPart.data as unknown as RedirectData} />}
                    {isLastPart && <MessageActions message={message} showFeedbackActions={!!showFeedbackActions} sourcesElement={sourcesElement} textContentOverride={reportContentOverride} />}
                </ChatBubbleMessage>
            </ChatBubble>
        );
    };

    const renderArtifactOrFilePart = (part: ArtifactPart | FilePart, index: number, isStreamingPart?: boolean) => {
        const uniqueKey = message.taskId
            ? `${message.taskId}-${part.kind === "file" ? (part as FilePart).file.name : (part as ArtifactPart).name}`
            : message.metadata?.messageId
              ? `${message.metadata.messageId}-${part.kind === "file" ? (part as FilePart).file.name : (part as ArtifactPart).name}`
              : undefined;

        if (part.kind === "file") {
            const filePart = part as FilePart;
            const fileInfo = filePart.file;
            const attachment: FileAttachment = {
                name: fileInfo.name || "untitled_file",
                mimeType: fileInfo.mimeType,
            };
            if ("bytes" in fileInfo && fileInfo.bytes) {
                attachment.content = fileInfo.bytes;
            } else if ("uri" in fileInfo && fileInfo.uri) {
                attachment.uri = fileInfo.uri;
            }
            return <ArtifactMessage key={`part-file-${index}`} status="completed" name={attachment.name} fileAttachment={attachment} uniqueKey={uniqueKey} message={message} />;
        }
        if (part.kind === "artifact") {
            const artifactPart = part as ArtifactPart;
            switch (artifactPart.status) {
                case "completed":
                    return <ArtifactMessage key={`part-artifact-${index}`} status="completed" name={artifactPart.name} fileAttachment={artifactPart.file!} uniqueKey={uniqueKey} message={message} />;
                case "in-progress":
                    return <ArtifactMessage key={`part-artifact-${index}`} status="in-progress" name={artifactPart.name} bytesTransferred={artifactPart.bytesTransferred!} uniqueKey={uniqueKey} isStreaming={isStreamingPart} message={message} />;
                case "failed":
                    return <ArtifactMessage key={`part-artifact-${index}`} status="failed" name={artifactPart.name} error={artifactPart.error} uniqueKey={uniqueKey} message={message} />;
                default:
                    return null;
            }
        }
        return null;
    };

    const lastPartIndex = groupedParts.length - 1;
    const lastPartKind = groupedParts[lastPartIndex]?.kind;

    const workflowChip = message.isUser && message.metadata?.chip?.kind === "workflow" ? message.metadata.chip : undefined;
    if (workflowChip) {
        return (
            <MessageWrapper message={message} className="flex justify-end">
                <div className="flex h-11 max-w-xs flex-shrink items-center gap-2 rounded-lg bg-(--secondary-w20) px-2 py-1">
                    <Workflow className="h-5 w-5 shrink-0" style={{ color: "var(--accent-n1-wMain)" }} />
                    <span className="min-w-0 flex-1 truncate text-sm leading-9" title={workflowChip.label}>
                        <strong>
                            <code>{workflowChip.label}</code>
                        </strong>
                    </span>
                </div>
            </MessageWrapper>
        );
    }

    return (
        <div key={message.metadata?.messageId} className="space-y-6">
            {!hideInlineActivityTimeline && !message.isUser && (!message.isComplete || (message.progressUpdates && message.progressUpdates.length > 0)) && (
                <div>
                    <InlineProgressUpdates
                        updates={showThinkingContentEnabled ? (message.progressUpdates ?? []) : (message.progressUpdates ?? []).filter(u => u.type !== "thinking")}
                        isActive={!message.isComplete && !!isLastWithTaskId}
                        allDone={!!message.isComplete}
                        onViewWorkflow={message.taskId ? handleViewWorkflowClick : undefined}
                    />
                </div>
            )}
            {message.isUser && message.contextQuote && (
                <div className="flex justify-end pr-4">
                    <button
                        className={`flex max-w-fit items-center gap-2 overflow-hidden rounded-md border bg-(--background-w20) px-3 py-2 text-sm ${message.contextQuoteSourceId ? "cursor-pointer transition-colors hover:bg-(--secondary-w20)" : "cursor-default"}`}
                        onClick={() => {
                            if (message.contextQuoteSourceId) {
                                window.dispatchEvent(
                                    new CustomEvent("scroll-to-message", {
                                        detail: { taskId: message.contextQuoteSourceId, quotedText: message.contextQuote },
                                    })
                                );
                            }
                        }}
                        disabled={!message.contextQuoteSourceId}
                        title={message.contextQuoteSourceId ? "Click to scroll to original message" : undefined}
                    >
                        <Quote className="h-4 w-4 flex-shrink-0 text-(--secondary-text-wMain)" />
                        <span className="truncate text-(--secondary-text-wMain) italic">"{message.contextQuote}"</span>
                    </button>
                </div>
            )}
            {shouldRenderUserInput && <UserInputMessage message={message} />}
            {groupedParts.map((part, index) => {
                const isLastPart = index === lastPartIndex;
                const shouldStream = isStreaming && isLastPart;

                if (part.kind === "text") {
                    return renderTextPart(part as TextPart, index, isLastPart, shouldStream);
                } else if (part.kind === "artifact" || part.kind === "file") {
                    return renderArtifactOrFilePart(part, index, shouldStream);
                } else if (part.kind === "build-plan") {
                    const bp = part as BuildPlanPart;
                    if (builderCreationState?.isBuilding && builderCreationState.components.length > 0) {
                        return (
                            <div key={`part-build-plan-${index}`} className="flex justify-start pl-4" data-testid="build-plan">
                                <BuilderProgressCard components={builderCreationState.components} isBuilding={builderCreationState.isBuilding} buildName={bp.filename?.replace(/\.ya?ml$/, "")} />
                            </div>
                        );
                    }
                    const hasFollowUpQuestion = !!message.userInputRequest && !message.userInputRequest.responded;
                    const shouldHideActions = hasFollowUpQuestion || !message.isComplete;
                    return (
                        <div key={`part-build-plan-${index}`} className="flex justify-start pl-4" data-testid="build-plan">
                            <BuildPlanCard filename={bp.filename} isComplete={bp.isComplete} hideActions={shouldHideActions} isMessageComplete={!!message.isComplete} />
                        </div>
                    );
                }
                return null;
            })}

            {redirectPart && message.isComplete && lastPartKind !== "text" && (
                <div className="flex justify-start pl-4">
                    <RedirectButton data={redirectPart.data as unknown as RedirectData} />
                </div>
            )}

            {deepResearchReportInfo && <DeepResearchReportBubble deepResearchReportInfo={deepResearchReportInfo} message={message} onContentLoaded={onReportContentLoaded} />}

            {lastPartKind === "artifact" || lastPartKind === "file" ? (
                <div className={`flex ${isRightAligned ? "justify-end pr-4" : "justify-start pl-4"}`}>
                    <MessageActions message={message} showFeedbackActions={!!showFeedbackActions} sourcesElement={sourcesElement} textContentOverride={reportContentOverride} />
                </div>
            ) : null}

            {message.isUser && !isOtherUserMessage(message, currentUserEmail) && (
                <div className="flex justify-end">
                    <MessageHoverButtons message={message} />
                </div>
            )}
        </div>
    );
};
export const ChatMessage = React.memo(function ChatMessage({ message, isLastWithTaskId, isStreaming, hideInlineActivityTimeline }: { message: MessageFE; isLastWithTaskId?: boolean; isStreaming?: boolean; hideInlineActivityTimeline?: boolean }) {
    const chatContext = useChatMessageData();
    const builderContext = useBuilderContext();
    const { ragData, openSidePanelTab, setTaskIdInSidePanel, sessionId, isCollaborativeSession, hasSharedEditors, currentUserEmail, agentNameDisplayNameMap, artifacts, allArtifacts } = chatContext;
    const { value: showThinkingContentEnabled } = useBooleanFlagDetails("show_thinking_content", false);

    const isOtherUser = message.isUser && isOtherUserMessage(message, currentUserEmail);
    const isRightAligned = message.isUser && !isOtherUser;

    const [reportContent, setReportContent] = useState<string | null>(null);

    const [isHighlighted, setIsHighlighted] = useState(false);
    const [highlightedText, setHighlightedText] = useState<string | null>(null);
    const messageRef = React.useRef<HTMLDivElement>(null);

    React.useEffect(() => {
        const handleScrollToMessage = (event: Event) => {
            const customEvent = event as CustomEvent;
            const { taskId, quotedText } = customEvent.detail;

            if (message.taskId === taskId && messageRef.current) {
                messageRef.current.scrollIntoView({ behavior: "smooth", block: "center" });

                if (quotedText) {
                    setHighlightedText(quotedText);
                    setTimeout(() => setHighlightedText(null), 2000);
                } else {
                    setIsHighlighted(true);
                    setTimeout(() => setIsHighlighted(false), 2000);
                }
            }
        };

        window.addEventListener("scroll-to-message", handleScrollToMessage);
        return () => {
            window.removeEventListener("scroll-to-message", handleScrollToMessage);
        };
    }, [message.taskId]);

    const taskRagData = useMemo(() => {
        if (!message?.taskId || !ragData) return undefined;
        return ragData.filter(r => r.taskId === message.taskId);
    }, [message?.taskId, ragData]);

    const deepResearchReportArtifact = useMemo(() => {
        if (!message) return null;

        const hasProgressPart = message.parts?.some(p => {
            if (p.kind === "data") {
                const data = (p as DataPart).data as unknown as ResearchProgressData;
                return data?.type === "deep_research_progress";
            }
            return false;
        });

        const hasRagSources = taskRagData && taskRagData.length > 0 && taskRagData.some(r => r.sources && r.sources.length > 0);
        const hasDeepResearchRagData = taskRagData?.some(r => r.searchType === "deep_research");
        const isDeepResearchComplete = message.isComplete && (hasProgressPart || hasDeepResearchRagData) && hasRagSources;

        if (!isDeepResearchComplete || !isLastWithTaskId) return null;

        const artifactParts = message.parts?.filter(p => p.kind === "artifact") as ArtifactPart[] | undefined;

        if (artifactParts && artifactParts.length > 0) {
            for (const part of artifactParts) {
                if (part.status === "completed" && isDeepResearchReportFilename(part.name)) {
                    const fullArtifact = artifacts.find(a => a.filename === part.name);
                    if (fullArtifact) {
                        return fullArtifact;
                    }
                }
            }
        }

        if (taskRagData && taskRagData.length > 0) {
            const lastRagData = taskRagData[taskRagData.length - 1];
            const artifactFilenameFromRag = lastRagData.metadata?.artifactFilename as string | undefined;
            if (artifactFilenameFromRag) {
                const matchedArtifact = artifacts.find(a => a.filename === artifactFilenameFromRag);
                if (matchedArtifact) {
                    return matchedArtifact;
                }
            }
        }

        const allReportArtifacts = artifacts.filter(a => isDeepResearchReportFilename(a.filename));
        if (allReportArtifacts.length === 1) {
            return allReportArtifacts[0];
        }

        return null;
    }, [message, isLastWithTaskId, artifacts, taskRagData]);

    const reportRagData = useMemo(() => {
        if (!taskRagData || taskRagData.length === 0) return undefined;
        return aggregateRagSources(taskRagData.filter(r => r.searchType === "deep_research"));
    }, [taskRagData]);

    if (!message) {
        return null;
    }

    if (message.isUser && message.metadata?.hidden === true) {
        return null;
    }

    const hasProgressPart = message.parts?.some(p => {
        if (p.kind === "data") {
            const data = (p as DataPart).data as unknown as ResearchProgressData;
            return data?.type === "deep_research_progress";
        }
        return false;
    });

    const hasRagSources = taskRagData && taskRagData.length > 0 && taskRagData.some(r => r.sources && r.sources.length > 0);

    const hasDeepResearchRagData = taskRagData?.some(r => r.searchType === "deep_research");

    const isDeepResearchComplete = message.isComplete && (hasProgressPart || hasDeepResearchRagData) && hasRagSources;

    const isNonDeepResearchComplete = message.isComplete && !isDeepResearchComplete && hasRagSources;

    const isWebSearchComplete = isNonDeepResearchComplete && taskRagData?.some(r => r.searchType === "web_search");

    const isDocumentSearchComplete = isNonDeepResearchComplete && hasDocumentSearchResults(taskRagData);

    const handleSourcesClick = () => {
        if (message.taskId) {
            setTaskIdInSidePanel(message.taskId);
            openSidePanelTab("rag");
        }
    };

    const agentDisplayName = (() => {
        if (!isCollaborativeSession || message.isUser) return undefined;
        const { selectedAgentName } = chatContext;
        if (selectedAgentName && agentNameDisplayNameMap[selectedAgentName]) {
            return agentNameDisplayNameMap[selectedAgentName];
        }
        if (selectedAgentName) {
            return selectedAgentName;
        }
        return undefined;
    })();

    const showAgentAttribution = (isCollaborativeSession || hasSharedEditors) && !message.isUser && !message.isStatusBubble;
    const hasAttribution = (message.isUser && isOtherUser) || showAgentAttribution;

    return (
        <div ref={messageRef} data-task-id={message.taskId} className={`transition-all duration-500 ${isHighlighted ? "rounded-lg bg-(--primary-w10) ring-2 ring-(--primary-w40)" : ""}`}>
            {(() => {
                if (message.isUser && isOtherUserMessage(message, currentUserEmail)) {
                    const displayName = message.senderDisplayName || message.senderEmail || "User";
                    const userIndex = getUserIndexFromEmail(message.senderEmail || "");
                    return <MessageAttribution type="user" name={displayName} userIndex={userIndex} timestamp={message.createdAt} />;
                }
                if (showAgentAttribution) {
                    const agentLabel = agentDisplayName || "AI Assistant";
                    return <MessageAttribution type="agent" name={agentLabel} timestamp={message.createdAt} />;
                }
                return null;
            })()}
            <div className={hasAttribution ? "ml-10" : ""}>
                {isDeepResearchComplete &&
                    hasRagSources &&
                    isLastWithTaskId &&
                    (() => {
                        const allSources = taskRagData.flatMap(r => r.sources);
                        const fetchedSources = allSources.filter(source => {
                            const wasFetched = source.metadata?.fetched === true || source.metadata?.fetch_status === "success" || (source.contentPreview && source.contentPreview.includes("[Full Content Fetched]"));
                            return wasFetched;
                        });

                        return (
                            <div className="mb-4">
                                <InlineResearchProgress
                                    progress={{
                                        type: "deep_research_progress",
                                        phase: "writing",
                                        status_text: "Research complete",
                                        progress_percentage: 100,
                                        current_iteration: 0,
                                        total_iterations: 0,
                                        sources_found: fetchedSources.length,
                                        current_query: "",
                                        fetching_urls: [],
                                        elapsed_seconds: 0,
                                        max_runtime_seconds: 0,
                                    }}
                                    isComplete={true}
                                    ragData={taskRagData}
                                />
                            </div>
                        );
                    })()}
                {getChatBubble(
                    message,
                    chatContext,
                    artifacts,
                    allArtifacts,
                    builderContext,
                    isLastWithTaskId,
                    isStreaming,
                    !message.isUser && (isDeepResearchComplete || isWebSearchComplete || isDocumentSearchComplete) && hasRagSources
                        ? (() => {
                              const allSources = taskRagData.flatMap(r => r.sources);

                              const sourcesToShow = isDeepResearchComplete
                                  ? allSources.filter(source => {
                                        const sourceType = source.sourceType || "web";
                                        if (sourceType === "image") {
                                            return source.sourceUrl || source.metadata?.link;
                                        }
                                        const wasFetched = source.metadata?.fetched === true || source.metadata?.fetch_status === "success" || (source.contentPreview && source.contentPreview.includes("[Full Content Fetched]"));
                                        return wasFetched;
                                    })
                                  : allSources.filter(source => {
                                        const sourceType = source.sourceType || "web";
                                        if (sourceType === "image") {
                                            return source.sourceUrl || source.metadata?.link;
                                        }
                                        return true;
                                    });

                              if (sourcesToShow.length === 0) return null;

                              return <Sources ragMetadata={{ sources: sourcesToShow }} isDeepResearch={isDeepResearchComplete} onDeepResearchClick={handleSourcesClick} />;
                          })()
                        : undefined,
                    isDeepResearchComplete && isLastWithTaskId && deepResearchReportArtifact && sessionId ? { artifact: deepResearchReportArtifact, sessionId, ragData: reportRagData } : undefined,
                    setReportContent,
                    reportContent || undefined,
                    highlightedText,
                    showThinkingContentEnabled,
                    hideInlineActivityTimeline
                )}

                {!message.isUser &&
                    isWebSearchComplete &&
                    hasRagSources &&
                    (() => {
                        const allSources = taskRagData.flatMap(r => r.sources);
                        const seenImageUrls = new Set<string>();
                        const imageResults = allSources
                            .filter(source => {
                                const sourceType = source.sourceType || "web";
                                if (sourceType !== "image" || !source.metadata?.imageUrl) return false;
                                const url = String(source.metadata.imageUrl);
                                if (seenImageUrls.has(url)) return false;
                                seenImageUrls.add(url);
                                return true;
                            })
                            .map(source => ({
                                imageUrl: source.metadata!.imageUrl,
                                title: source.metadata?.title || source.filename,
                                link: source.sourceUrl || source.metadata?.link || source.metadata!.imageUrl,
                            }));

                        if (imageResults.length > 0) {
                            return (
                                <div className="mt-4">
                                    <ImageSearchGrid images={imageResults} />
                                </div>
                            );
                        }
                        return null;
                    })()}

                {getUploadedFiles(message, isRightAligned)}
            </div>
        </div>
    );
});
