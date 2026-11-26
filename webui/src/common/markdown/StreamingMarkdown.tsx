// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Citation } from "@/utils/citations";

import { MarkdownHTMLConverter } from "./MarkdownHTMLConverter";
import { TextWithCitations } from "./TextWithCitations";
import { useStreamingAnimation } from "./useStreamingAnimation";
import { useStreamingSpeed } from "./useStreamingSpeed";

interface StreamingMarkdownProps {
    content: string;
    className?: string;
    citations?: Citation[];
    onCitationClick?: (citation: Citation) => void;
}

const StreamingMarkdown = ({ content, className, citations, onCitationClick }: StreamingMarkdownProps) => {
    const { state, contentRef } = useStreamingSpeed(content);
    const displayedContent = useStreamingAnimation(state, contentRef);

    if (citations && citations.length > 0) {
        return <TextWithCitations text={displayedContent} citations={citations} onCitationClick={onCitationClick} />;
    }

    return <MarkdownHTMLConverter className={className}>{displayedContent}</MarkdownHTMLConverter>;
};

export { StreamingMarkdown };
