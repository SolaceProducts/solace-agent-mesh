// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useState } from "react";
import { X } from "lucide-react";

import { useSkillFileContent } from "@/api/skills";

import { Button } from "@/ui/button";

import { EmptyState, MarkdownHTMLConverter, MessageBanner } from "@/common";

interface SkillFilePreviewProps {
    skillId: string;
    path: string;
    title: string;
    onClose: () => void;
}

export const SkillFilePreview: React.FC<SkillFilePreviewProps> = ({ skillId, path, title, onClose }) => {
    const { data, isLoading, error } = useSkillFileContent(skillId, path);
    const [objectUrl, setObjectUrl] = useState<string | null>(null);

    useEffect(() => {
        if (!data?.blob) {
            setObjectUrl(null);
            return;
        }
        const url = URL.createObjectURL(data.blob);
        setObjectUrl(url);
        return () => URL.revokeObjectURL(url);
    }, [data?.blob]);

    const isMarkdown = !!data && (data.contentType.toLowerCase().includes("markdown") || title.toLowerCase().endsWith(".md"));

    return (
        <div className="flex h-full min-h-0 flex-col border-l">
            <div className="flex items-center justify-between gap-2 border-b p-4">
                <div className="truncate font-medium" title={title}>
                    {title}
                </div>
                <Button variant="ghost" onClick={onClose} tooltip="Close preview" className="h-6 w-6">
                    <X className="size-4" />
                </Button>
            </div>
            <div className="min-h-0 flex-1 overflow-auto p-4">
                {isLoading && <EmptyState variant="loading" title="Loading file..." />}
                {error && <MessageBanner variant="error" message={`Failed to load file. ${error instanceof Error ? error.message : String(error)}`} />}
                {data && !isLoading && !error && (
                    <>
                        {data.text !== undefined && (isMarkdown ? <MarkdownHTMLConverter>{data.text}</MarkdownHTMLConverter> : <pre className="text-sm break-words whitespace-pre-wrap">{data.text}</pre>)}
                        {objectUrl && <img src={objectUrl} alt={title} className="max-w-full" />}
                        {data.text === undefined && !data.blob && <div className="text-sm text-(--secondary-text-wMain)">Preview not available for this file type ({data.contentType || "unknown"}).</div>}
                    </>
                )}
            </div>
        </div>
    );
};
