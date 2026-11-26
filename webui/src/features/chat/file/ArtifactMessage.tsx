// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useCallback, useEffect, useMemo, useState } from "react";
import { useMutation } from "@tanstack/react-query";

import { api, getErrorFromResponse } from "@/api/client";

import { useChatMessageData, useProjectContext } from "@/providers/contexts";

import { isDeepResearchReportFilename } from "@/utils/deepResearchUtils";

import { Spinner } from "@/ui/spinner";

import { useArtifactRendering } from "@/features/chat/hooks";

import { MessageBanner } from "@/common";
import type { FileAttachment, MessageFE } from "@/types";
import { downloadFile, getErrorMessage, getSourceProjectName, parseArtifactUri } from "@/utils";

import { ArtifactBar } from "../artifact/ArtifactBar";
import { ArtifactTransitionOverlay } from "../artifact/ArtifactTransitionOverlay";
import { ContentRenderer } from "../preview/ContentRenderer";
import { getFileContent, getRenderType } from "../preview/previewUtils";

import { isArtifactDeleted } from "./artifactMessageUtils";
import { FileDetails } from "./FileDetails";

type ArtifactMessageProps = (
    | {
          status: "in-progress";
          name: string;
          bytesTransferred: number;
      }
    | {
          status: "completed";
          name: string;
          fileAttachment: FileAttachment;
      }
    | {
          status: "failed";
          name: string;
          error?: string;
      }
) & {
    context?: "chat" | "list";
    uniqueKey?: string;
    isStreaming?: boolean;
    message?: MessageFE;
    readOnly?: boolean;
    onDownloadOverride?: () => Promise<void>;
};

export const ArtifactMessage = (props: ArtifactMessageProps) => {
    const { setPreviewArtifact, openSidePanelTab, sessionId, openDeleteModal, markArtifactAsDisplayed, downloadAndResolveArtifact, navigateArtifactVersion, ragData, addNotification, displayError, artifacts, allArtifacts } = useChatMessageData();
    const { activeProject } = useProjectContext();
    const [isLoading, setIsLoading] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const [fetchedContent, setFetchedContent] = useState<string | null>(null);
    const [renderError, setRenderError] = useState<string | null>(null);
    const [isInfoExpanded, setIsInfoExpanded] = useState(false);
    const [isDownloading, setIsDownloading] = useState(false);

    const artifact = useMemo(() => artifacts.find(art => art.filename === props.name), [artifacts, props.name]);
    const context = props.context || "chat";
    const isStreaming = props.isStreaming;
    const readOnly = props.readOnly || artifact?.source === "project" || false;

    const version = useMemo(() => {
        const fileAttachment = props.status === "completed" ? props.fileAttachment : undefined;
        if (fileAttachment?.uri) {
            const parsed = parseArtifactUri(fileAttachment.uri);
            return parsed?.version !== null && parsed?.version !== undefined ? parseInt(parsed.version) : undefined;
        }
        return undefined;
    }, [props]);

    const fileAttachment = props.status === "completed" ? props.fileAttachment : undefined;
    const fileName = fileAttachment?.name || props.name;
    const fileMimeType = fileAttachment?.mimeType || artifact?.mimeType;

    const artifactInAll = useMemo(() => (allArtifacts ?? artifacts).find(art => art.filename === props.name), [allArtifacts, artifacts, props.name]);

    const isDeleted = useMemo(() => isArtifactDeleted({ status: props.status, artifactInfo: artifactInAll, fileAttachment, message: props.message }), [props.status, artifactInAll, fileAttachment, props.message]);

    const shouldAutoExpand = useMemo(() => {
        if (isDeleted) {
            return false;
        }

        if (isDeepResearchReportFilename(fileName)) {
            return false;
        }

        const renderType = getRenderType(fileName, fileMimeType);
        const isAutoRenderType = renderType === "image" || renderType === "audio" || renderType === "markdown";

        const isTxtFile = fileName.toLowerCase().endsWith(".txt") || fileName.toLowerCase().endsWith(".text");
        const shouldAutoExpandText = renderType === "text" && isTxtFile;

        return (isAutoRenderType || shouldAutoExpandText) && context === "chat";
    }, [fileName, fileMimeType, context, isDeleted]);

    const { shouldRender, isExpandable, isExpanded, toggleExpanded } = useArtifactRendering({
        filename: fileName,
        mimeType: fileMimeType,
        shouldAutoExpand,
    });

    const handlePreviewClick = useCallback(async () => {
        const artifactToPreview = artifact || artifactInAll;
        if (artifactToPreview) {
            openSidePanelTab("files");
            setPreviewArtifact(artifactToPreview);

            if (version !== undefined) {
                setTimeout(async () => {
                    await navigateArtifactVersion(artifactToPreview.filename, version);
                }, 100);
            }
        }
    }, [artifact, artifactInAll, openSidePanelTab, setPreviewArtifact, version, navigateArtifactVersion]);

    const downloadMutation = useMutation({
        mutationFn: async () => {
            if (props.onDownloadOverride) {
                await props.onDownloadOverride();
                return;
            }

            let fileToDownload: FileAttachment | null = null;

            const artifactSource = artifact || artifactInAll;
            if (artifactSource) {
                fileToDownload = {
                    name: artifactSource.filename,
                    mimeType: artifactSource.mimeType,
                    uri: artifactSource.uri,
                    size: artifactSource.size,
                    lastModified: artifactSource.lastModified,
                };
                if (!fileToDownload.uri) {
                    if (fetchedContent) {
                        fileToDownload.content = fetchedContent;
                    } else if (fileAttachment?.content) {
                        fileToDownload.content = fileAttachment.content;
                    }
                }
            } else if (fileAttachment) {
                fileToDownload = fileAttachment;
            }

            if (!fileToDownload) {
                console.error(`No file to download for artifact: ${props.name}`);
                return;
            }
            if (await downloadFile(fileToDownload, sessionId, activeProject?.id)) {
                addNotification(`Downloaded ${fileName}.`, "success");
            } else {
                displayError({ title: "Failed to Download Artifact", error: `Could not download ${fileName}.`, errorSource: "api" });
            }
        },
        onError: error => displayError({ title: "Failed to Download Artifact", error: getErrorMessage(error, "An unknown error occurred while downloading the artifact."), errorSource: "api" }),
    });

    const handleDownloadClick = useCallback(() => {
        downloadMutation.mutate();
    }, [downloadMutation]);

    const downloading = downloadMutation.isPending;

    const handleDeleteClick = useCallback(() => {
        if (artifact) {
            openDeleteModal(artifact);
        }
    }, [artifact, openDeleteModal]);

    const handleInfoClick = useCallback(() => {
        setIsInfoExpanded(!isInfoExpanded);
    }, [isInfoExpanded]);

    useEffect(() => {
        const filename = artifact?.filename;
        if (shouldRender && filename) {
            markArtifactAsDisplayed(filename, true);
        }

        return () => {
            if (filename) {
                markArtifactAsDisplayed(filename, false);
            }
        };
    }, [shouldRender, artifact?.filename, markArtifactAsDisplayed]);

    const isImage = useMemo(() => {
        const renderType = getRenderType(fileName, fileMimeType);
        return renderType === "image";
    }, [fileName, fileMimeType]);

    const isTextOrMarkdown = useMemo(() => {
        const renderType = getRenderType(fileName, fileMimeType);
        return renderType === "text" || renderType === "markdown";
    }, [fileName, fileMimeType]);

    useEffect(() => {
        if (props.status === "in-progress" && artifact?.accumulatedContent && shouldRender) {
            setFetchedContent(artifact.accumulatedContent);
        }
    }, [artifact?.accumulatedContent, props.status, fileName, shouldRender, isExpanded]);

    useEffect(() => {
        const triggerDownload = async () => {
            if (artifact?.needsEmbedResolution && props.status === "completed" && shouldRender && !isDownloading) {
                setIsDownloading(true);
                try {
                    const fileData = await downloadAndResolveArtifact(artifact.filename);
                    if (fileData?.content) {
                        setFetchedContent(fileData.content);
                    }
                } catch (err) {
                    console.error(`Error downloading ${fileName}:`, err);
                } finally {
                    setIsDownloading(false);
                }
            }
        };

        triggerDownload();
    }, [artifact?.needsEmbedResolution, props.status, shouldRender, fileName, artifact?.filename, downloadAndResolveArtifact, isDownloading]);

    useEffect(() => {
        const fetchContentFromUri = async () => {
            if (isLoading || !shouldRender || error) {
                return;
            }

            if (props.status === "in-progress") {
                if (artifact?.accumulatedContent) {
                    setFetchedContent(artifact.accumulatedContent);
                }
                return;
            }

            if (props.status !== "completed") {
                return;
            }

            if (artifact?.accumulatedContent) {
                setFetchedContent(artifact.accumulatedContent);
                return;
            }

            const fileContent = fileAttachment?.content;
            if (fetchedContent || fileContent) {
                if (fileContent && !fetchedContent) {
                    setFetchedContent(fileContent);
                }
                return;
            }

            const fileUri = fileAttachment?.uri;
            if (!fileUri) {
                return;
            }

            setIsLoading(true);
            setError(null);

            try {
                const parsedUri = parseArtifactUri(fileUri);
                if (!parsedUri) throw new Error("Invalid artifact URI.");

                const { sessionId: uriSessionId, filename, version } = parsedUri;

                let apiUrl: string;
                const effectiveSessionId = uriSessionId || sessionId;
                if (effectiveSessionId && effectiveSessionId.trim() && effectiveSessionId !== "null" && effectiveSessionId !== "undefined") {
                    apiUrl = `/api/v1/artifacts/${effectiveSessionId}/${encodeURIComponent(filename)}/versions/${version || "latest"}`;
                } else if (activeProject?.id) {
                    apiUrl = `/api/v1/artifacts/null/${encodeURIComponent(filename)}/versions/${version || "latest"}?projectId=${activeProject.id}`;
                } else {
                    apiUrl = `/api/v1/artifacts/null/${encodeURIComponent(filename)}/versions/${version || "latest"}`;
                }

                const response = await api.webui.get(apiUrl, { fullResponse: true });
                if (!response.ok) {
                    const errorMessage = await getErrorFromResponse(response);
                    throw new Error(`Failed to fetch artifact content: ${errorMessage}`);
                }

                const blob = await response.blob();
                const base64data = await new Promise<string>((resolve, reject) => {
                    const reader = new FileReader();
                    reader.onloadend = () => {
                        if (typeof reader.result === "string") {
                            resolve(reader.result.split(",")[1]);
                        } else {
                            reject(new Error("Failed to read artifact content as a data URL."));
                        }
                    };
                    reader.onerror = () => {
                        reject(reader.error || new Error("An unknown error occurred while reading the file."));
                    };
                    reader.readAsDataURL(blob);
                });

                setFetchedContent(base64data);
            } catch (e) {
                setError(getErrorMessage(e, "Failed to fetch artifact content."));
            } finally {
                setIsLoading(false);
            }
        };

        fetchContentFromUri();
    }, [props.status, shouldRender, fileAttachment, sessionId, activeProject?.id, isLoading, fetchedContent, artifact?.accumulatedContent, fileName, isExpanded, artifact, error]);

    const taskRagData = useMemo(() => {
        if (!props.message?.taskId || !ragData) return undefined;
        return ragData.find(r => r.taskId === props.message?.taskId);
    }, [props.message?.taskId, ragData]);

    const actions = useMemo(() => {
        if (props.status === "failed") return undefined;

        if (context === "list") {
            return {
                onInfo: handleInfoClick,
                onDownload: props.status === "completed" ? handleDownloadClick : undefined,
                onDelete: artifact && props.status === "completed" && !readOnly ? handleDeleteClick : undefined,
            };
        } else {
            return {
                onPreview: props.status === "completed" ? handlePreviewClick : undefined,
                onDownload: props.status === "completed" ? handleDownloadClick : undefined,
                onInfo: handleInfoClick,
            };
        }
    }, [props.status, context, handleDownloadClick, artifact, handleDeleteClick, handleInfoClick, handlePreviewClick, readOnly]);

    const description = artifactInAll?.description;

    const contentToRender = fetchedContent || fileAttachment?.content;
    const renderType = getRenderType(fileName, fileMimeType);

    let expandedContent: ReactNode = null;

    if (isLoading) {
        expandedContent = (
            <div className="flex h-25 items-center justify-center bg-(--secondary-w10)">
                <Spinner />
            </div>
        );
    } else if (error) {
        expandedContent = <MessageBanner variant="error" message={error} />;
    } else if (contentToRender && renderType) {
        try {
            const fileForRendering: FileAttachment = fileAttachment || {
                name: fileName,
                mimeType: fileMimeType,
            };

            const finalContent = getFileContent({
                ...fileForRendering,
                content: contentToRender,
                // @ts-expect-error - Add flag to indicate if content is plain text from streaming
                isPlainText: (artifact?.isAccumulatedContentPlainText && fetchedContent === artifact?.accumulatedContent) || (props.status === "in-progress" && !!fetchedContent),
            });

            if (finalContent) {
                let maxHeight: string;
                let height: string | undefined;
                let overflowY: "visible" | "auto";

                if (isImage) {
                    maxHeight = "none";
                    overflowY = "visible";
                } else if (isTextOrMarkdown) {
                    maxHeight = "6000px";
                    overflowY = "auto";
                } else if (renderType === "audio") {
                    maxHeight = "300px";
                    overflowY = "auto";
                } else if (renderType === "html") {
                    height = "600px";
                    maxHeight = "600px";
                    overflowY = "auto";
                } else {
                    maxHeight = "600px";
                    overflowY = "auto";
                }

                expandedContent = (
                    <div className="group relative max-w-full overflow-hidden">
                        {renderError && <MessageBanner variant="error" message={renderError} />}
                        <div
                            style={{
                                height,
                                maxHeight,
                                overflowY,
                            }}
                            className={isImage ? "drop-shadow-md" : ""}
                        >
                            <ContentRenderer content={finalContent} rendererType={renderType} mimeType={fileMimeType} setRenderError={setRenderError} isStreaming={isStreaming} ragData={taskRagData} />
                        </div>
                        <ArtifactTransitionOverlay isVisible={isDownloading} message="Resolving embeds..." />
                    </div>
                );
            }
        } catch (error) {
            console.error("Failed to process file content:", error);
            expandedContent = <MessageBanner variant="error" message="Failed to process file content for rendering" />;
        }
    }

    const shouldShowContent = shouldRender && isExpanded;

    const infoContent = useMemo(() => {
        const artifactData = artifactInAll || artifact;
        if (!isInfoExpanded || !artifactData) return null;

        return <FileDetails description={artifactData.description ?? undefined} size={artifactData.size} lastModified={artifactData.lastModified} mimeType={artifactData.mimeType} />;
    }, [isInfoExpanded, artifact, artifactInAll]);

    const finalExpandedContent = useMemo(() => {
        const hasInfo = isInfoExpanded && infoContent;
        const hasContent = shouldShowContent && expandedContent;

        if (hasInfo && hasContent) {
            return (
                <div className="space-y-4">
                    {infoContent}
                    <hr className="border-t" />
                    {expandedContent}
                </div>
            );
        }

        if (hasInfo) {
            return infoContent;
        }

        if (hasContent) {
            return expandedContent;
        }

        return undefined;
    }, [isInfoExpanded, infoContent, shouldShowContent, expandedContent]);

    return (
        <ArtifactBar
            filename={fileName}
            description={description || ""}
            mimeType={fileMimeType}
            size={fileAttachment?.size}
            status={props.status}
            expandable={isExpandable && context === "chat" && !isDeepResearchReportFilename(fileName)}
            expanded={isExpanded || isInfoExpanded}
            onToggleExpand={isExpandable && context === "chat" ? toggleExpanded : undefined}
            actions={actions}
            downloading={downloading}
            bytesTransferred={props.status === "in-progress" ? props.bytesTransferred : undefined}
            error={props.status === "failed" ? props.error : undefined}
            expandedContent={finalExpandedContent}
            expandedContentSelectable={isInfoExpanded || (renderType !== "image" && renderType !== "audio")}
            context={context}
            isDeleted={isDeleted}
            version={version}
            source={artifactInAll?.source}
            sourceProjectName={getSourceProjectName(artifactInAll, activeProject)}
        />
    );
};
