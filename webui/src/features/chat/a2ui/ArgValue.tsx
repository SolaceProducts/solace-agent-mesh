// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo, useState } from "react";
import DOMPurify from "dompurify";

import { CopyButton as CopyButtonBase } from "@/common/buttons/CopyButton";

import { ContentRenderer } from "@/features/chat/preview/ContentRenderer";
import { containsHtmlContent } from "@/features/chat/preview/contentUtils";

import { cn } from "@/utils";

const RICH_TYPES = new Set(["html", "json", "yaml", "markdown"]);

export function isRichArgType(type: string): boolean {
    return RICH_TYPES.has(type);
}

function sniffContentType(text: string): string {
    if (containsHtmlContent(text)) return "html";
    const trimmed = text.trim();
    const isBracketed = (trimmed.startsWith("{") && trimmed.endsWith("}")) || (trimmed.startsWith("[") && trimmed.endsWith("]"));
    if (isBracketed) {
        try {
            const parsed: unknown = JSON.parse(trimmed);
            if (parsed && typeof parsed === "object") return "json";
        } catch {}
    }
    return "text";
}

export function resolveArgRenderType(value: string, contentType?: string): string {
    const hint = contentType?.toLowerCase();
    if (hint && (RICH_TYPES.has(hint) || hint === "text")) return hint;
    return sniffContentType(value);
}

const INERT_HTML_PREVIEW_SANDBOX = "";

function SanitizedHtmlPreview({ value, tooltip }: Readonly<{ value: string; tooltip?: string }>) {
    const sanitized = useMemo(() => DOMPurify.sanitize(value), [value]);
    return (
        <div className="h-64 overflow-hidden rounded border" title={tooltip}>
            <iframe srcDoc={sanitized} title="HTML argument preview" sandbox={INERT_HTML_PREVIEW_SANDBOX} className="h-full w-full border-none bg-white" />
        </div>
    );
}

function CopyButton({ value }: Readonly<{ value: string }>) {
    return <CopyButtonBase value={value} copiedLabel="Copied!" className="absolute top-1 right-1 z-10 bg-(--background-w10)/80 opacity-0 transition-opacity group-hover:opacity-100 hover:bg-(--background-w10)" />;
}

interface ArgValueProps {
    value: string;
    contentType?: string;
    tooltip?: string;
    showCopy?: boolean;
}

export function ArgValue({ value, contentType, tooltip, showCopy = true }: Readonly<ArgValueProps>) {
    const [renderError, setRenderError] = useState<string | null>(null);

    const resolvedType = useMemo(() => resolveArgRenderType(value, contentType), [contentType, value]);

    if (resolvedType === "html") {
        return (
            <div className="group relative">
                {showCopy && <CopyButton value={value} />}
                <SanitizedHtmlPreview value={value} tooltip={tooltip} />
            </div>
        );
    }

    const showRich = isRichArgType(resolvedType) && !renderError;

    if (!showRich) {
        return (
            <div className="group relative">
                {showCopy && <CopyButton value={value} />}
                <div className="max-h-40 overflow-y-auto rounded border px-2 py-1 font-mono text-sm break-words whitespace-pre-wrap" title={tooltip}>
                    {value}
                </div>
            </div>
        );
    }

    const heightClass = resolvedType === "markdown" ? "max-h-80" : "max-h-64";

    return (
        <div className="group relative">
            {showCopy && <CopyButton value={value} />}
            <div className={cn("overflow-auto rounded border", heightClass)} title={tooltip}>
                <ContentRenderer content={value} rendererType={resolvedType} setRenderError={setRenderError} />
            </div>
        </div>
    );
}
