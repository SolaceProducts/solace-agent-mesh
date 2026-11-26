// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { getRenderType } from "./previewUtils";

const mimeTypeToExtension: Record<string, string> = {
    "application/pdf": "pdf",
    "application/zip": "zip",
    "application/msword": "doc",
    "application/vnd.openxmlformats-officedocument.wordprocessingml.document": "docx",
    "application/vnd.ms-excel": "xls",
    "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": "xlsx",
    "application/vnd.ms-powerpoint": "ppt",
    "application/vnd.openxmlformats-officedocument.presentationml.presentation": "pptx",
    "text/plain": "txt",
    "text/csv": "csv",
    "text/html": "html",
    "text/markdown": "md",
    "text/x-markdown": "md",
    "application/json": "json",
    "application/yaml": "yaml",
    "text/yaml": "yaml",
    "application/xml": "xml",
    "text/xml": "xml",
    "image/jpeg": "jpg",
    "image/png": "png",
    "image/gif": "gif",
    "image/svg+xml": "svg",
    "image/webp": "webp",
    "image/bmp": "bmp",
    "image/tiff": "tiff",
    "audio/mpeg": "mp3",
    "audio/wav": "wav",
    "audio/ogg": "ogg",
    "audio/aac": "aac",
    "audio/flac": "flac",
    "audio/x-m4a": "m4a",
    "video/mp4": "mp4",
    "video/webm": "webm",
    "video/ogg": "ogv",
    "application/javascript": "js",
    "application/gzip": "gz",
    "application/x-tar": "tar",
    "application/rtf": "rtf",
};

function generateFilenameFromMimeType(mimeType: string, index: number): string {
    const extension = mimeTypeToExtension[mimeType] || "bin";
    return `embedded_file_${index + 1}.${extension}`;
}

export interface ExtractedContent {
    type: string;
    content: string;
    mimeType?: string;
    originalMatch: string;
    filename?: string;
}

export function extractDataUris(text: string): ExtractedContent[] {
    if (!text || typeof text !== "string") {
        return [];
    }

    const results: ExtractedContent[] = [];
    const dataUriRegex = /data:([a-zA-Z0-9/.-]+);base64,([A-Za-z0-9+/=]+)/g;

    let match;
    let fileCounter = 0;
    while ((match = dataUriRegex.exec(text)) !== null) {
        const [fullMatch, mimeType, base64Data] = match;

        const charBefore = text[match.index - 1];
        const charAfter = text[match.index + fullMatch.length];

        if ((charBefore === '"' && charAfter === '"') || (charBefore === "'" && charAfter === "'")) {
            continue;
        }

        if (base64Data && base64Data.length > 10) {
            const renderType = getRenderType(undefined, mimeType);

            if (renderType) {
                results.push({
                    type: renderType,
                    content: base64Data,
                    mimeType: mimeType,
                    originalMatch: fullMatch,
                });
            } else {
                results.push({
                    type: "file",
                    content: base64Data,
                    mimeType: mimeType,
                    originalMatch: fullMatch,
                    filename: generateFilenameFromMimeType(mimeType, fileCounter++),
                });
            }
        }
    }

    return results;
}

export function containsHtmlContent(text: string): boolean {
    if (!text || typeof text !== "string") {
        return false;
    }

    const htmlRegex = /<\/?[a-z][\s\S]*?>/i;

    const isInCodeBlock = /```[\s\S]*?```|`[\s\S]*?`/g.test(text);

    return htmlRegex.test(text) && !isInCodeBlock;
}

export function processEmbeddedContent(text: string): string {
    if (!text || typeof text !== "string") {
        return text || "";
    }

    const processedText = text;

    return processedText;
}

export function containsMermaidDiagram(text: string): boolean {
    if (!text || typeof text !== "string") {
        return false;
    }

    const mermaidRegex = /```mermaid\s*\n([\s\S]*?)```/i;
    return mermaidRegex.test(text);
}

export function containsEmbeddedContent(text: string): boolean {
    const dataUriRegex = /data:([a-zA-Z0-9/.-]+);base64,([A-Za-z0-9+/=]+)/;
    return dataUriRegex.test(text) || containsHtmlContent(text) || containsMermaidDiagram(text);
}

export function extractHtmlContent(text: string): ExtractedContent[] {
    if (!text || typeof text !== "string") {
        return [];
    }

    const results: ExtractedContent[] = [];
    const htmlRegex = /<html[\s\S]*?<\/html>/gi;

    let match;
    while ((match = htmlRegex.exec(text)) !== null) {
        results.push({
            type: "html",
            content: match[0].trim(),
            originalMatch: match[0],
        });
    }

    return results;
}

export function extractMermaidDiagrams(text: string): ExtractedContent[] {
    if (!text || typeof text !== "string") {
        return [];
    }

    const results: ExtractedContent[] = [];
    const mermaidRegex = /```mermaid\s*\n([\s\S]*?)```/gi;

    let match;
    while ((match = mermaidRegex.exec(text)) !== null) {
        const [fullMatch, diagramContent] = match;

        results.push({
            type: "mermaid",
            content: diagramContent.trim(),
            originalMatch: fullMatch,
        });
    }

    return results;
}

export function extractEmbeddedContent(text: string): ExtractedContent[] {
    if (!text || typeof text !== "string") {
        return [];
    }

    return [...extractDataUris(text), ...extractHtmlContent(text), ...extractMermaidDiagrams(text)];
}
