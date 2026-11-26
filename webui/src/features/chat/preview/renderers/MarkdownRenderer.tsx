// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useMemo } from "react";

import { MarkdownWrapper, TextWithCitations } from "@/common/markdown";

import { parseCitations } from "@/utils/citations";
import { getThemeHtmlStyles } from "@/utils/themeHtmlStyles";

import { useCitationClick } from "@/features/chat/hooks";

import type { RAGSearchResult } from "@/types";

import { useCopy } from "../../../../hooks/useCopy";

import type { BaseRendererProps } from ".";

interface MarkdownRendererProps extends BaseRendererProps {
    ragData?: RAGSearchResult;
}

export const MarkdownRenderer: React.FC<MarkdownRendererProps> = ({ content, ragData, isStreaming }) => {
    const { ref, handleKeyDown } = useCopy<HTMLDivElement>();
    const citations = useMemo(() => parseCitations(content, ragData), [content, ragData]);
    const handleCitationClick = useCitationClick(ragData?.taskId);

    return (
        <div className="w-full p-4">
            <div ref={ref} className="max-w-full overflow-hidden select-text focus-visible:outline-none" tabIndex={0} onKeyDown={handleKeyDown}>
                {isStreaming ? (
                    <MarkdownWrapper content={content} isStreaming={isStreaming} className="max-w-full break-words" />
                ) : (
                    <div className={getThemeHtmlStyles("max-w-full break-words")}>
                        <TextWithCitations text={content} citations={citations} onCitationClick={handleCitationClick} />
                    </div>
                )}
            </div>
        </div>
    );
};
