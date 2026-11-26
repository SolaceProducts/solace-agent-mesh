// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import type { Citation } from "@/utils/citations";

import { MarkdownHTMLConverter } from "./MarkdownHTMLConverter";
import { StreamingMarkdown } from "./StreamingMarkdown";
import { TextWithCitations } from "./TextWithCitations";

interface MarkdownWrapperProps {
    content: string;
    isStreaming?: boolean;
    className?: string;
    citations?: Citation[];
    onCitationClick?: (citation: Citation) => void;
}

const MarkdownWrapper: React.FC<MarkdownWrapperProps> = ({ content, isStreaming, className, citations, onCitationClick }) => {
    if (isStreaming) {
        return <StreamingMarkdown content={content} className={className} citations={citations} onCitationClick={onCitationClick} />;
    }

    if (citations && citations.length > 0) {
        return <TextWithCitations text={content} citations={citations} onCitationClick={onCitationClick} />;
    }

    return <MarkdownHTMLConverter className={className}>{content}</MarkdownHTMLConverter>;
};

export { MarkdownWrapper };
