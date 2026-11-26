// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo, useState } from "react";

import { api } from "@/api/client";

import { isDeepResearchReportFilename, stripReportMetadataSections } from "@/utils/deepResearchUtils";

import { MarkdownRenderer } from "@/features/chat/preview/renderers";

import type { ArtifactInfo, RAGSearchResult } from "@/types";
import { parseArtifactUri } from "@/utils";

interface DeepResearchReportContentProps {
    artifact: ArtifactInfo;
    sessionId: string;
    ragData?: RAGSearchResult;
    onContentLoaded?: (content: string) => void;
}

export const DeepResearchReportContent = ({ artifact, sessionId, ragData, onContentLoaded }: DeepResearchReportContentProps) => {
    const [content, setContent] = useState<string | null>(null);
    const [loadedFilename, setLoadedFilename] = useState<string | null>(null);
    const [isLoading, setIsLoading] = useState(false);
    const [error, setError] = useState<string | null>(null);

    const isDeepResearchReport = useMemo(() => {
        return isDeepResearchReportFilename(artifact.filename);
    }, [artifact.filename]);

    useEffect(() => {
        if (loadedFilename && loadedFilename !== artifact.filename) {
            setContent(null);
            setLoadedFilename(null);
            setError(null);
        }
    }, [artifact.filename, loadedFilename]);

    useEffect(() => {
        const fetchContent = async () => {
            if (!isDeepResearchReport) {
                return;
            }

            if (content && loadedFilename === artifact.filename) {
                return;
            }

            if (artifact.accumulatedContent) {
                setContent(artifact.accumulatedContent);
                setLoadedFilename(artifact.filename);
                return;
            }

            const fileUri = artifact.uri;
            if (!fileUri) {
                return;
            }

            setIsLoading(true);
            setError(null);

            try {
                const parsedUri = parseArtifactUri(fileUri);
                if (!parsedUri) {
                    throw new Error("Invalid artifact URI.");
                }

                const { sessionId: uriSessionId, filename, version } = parsedUri;
                const effectiveSessionId = uriSessionId || sessionId;

                const apiUrl = `/api/v1/artifacts/${effectiveSessionId}/${encodeURIComponent(filename)}/versions/${version || "latest"}`;

                const response = await api.webui.get(apiUrl, { fullResponse: true });
                if (!response.ok) {
                    throw new Error(`Failed to fetch artifact content: ${response.statusText}`);
                }

                const text = await response.text();
                setContent(text);
                setLoadedFilename(artifact.filename);
            } catch (e) {
                console.error("Error fetching deep research report content:", e);
                setError(e instanceof Error ? e.message : "Unknown error fetching content.");
            } finally {
                setIsLoading(false);
            }
        };

        fetchContent();
    }, [isDeepResearchReport, artifact, sessionId, content, loadedFilename]);

    const filteredContent = useMemo(() => {
        if (!content) {
            return null;
        }
        return stripReportMetadataSections(content);
    }, [content]);

    useEffect(() => {
        if (filteredContent && onContentLoaded) {
            onContentLoaded(filteredContent);
        }
    }, [filteredContent, onContentLoaded]);

    if (!isDeepResearchReport) {
        return null;
    }

    if (isLoading) {
        return <div className="py-2 text-sm text-(--secondary-text-wMain)">Loading report...</div>;
    }

    if (error) {
        return <div className="py-2 text-sm text-(--error-wMain)">{error}</div>;
    }

    if (!filteredContent) {
        return null;
    }

    return <MarkdownRenderer content={filteredContent} setRenderError={() => {}} ragData={ragData} />;
};

export default DeepResearchReportContent;
