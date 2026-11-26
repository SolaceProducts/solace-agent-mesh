// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import DOMPurify from "dompurify";
import { File, FileAudio, FileCode, FileImage, FileJson, FileSpreadsheet, FileText } from "lucide-react";

import type { ArtifactInfo } from "@/types";

import { getRenderType } from "../preview/previewUtils";

export const getFileIcon = (artifact: ArtifactInfo | undefined, className = "h-4 w-4") => {
    if (!artifact) {
        return <File className={className} />;
    }

    const type = getRenderType(artifact.filename, artifact.mimeType);

    switch (type) {
        case "image":
        case "mermaid":
            return <FileImage className={className} />;
        case "audio":
            return <FileAudio className={className} />;
        case "html":
            return <FileCode className={className} />;
        case "text":
            return <FileText className={className} />;
        case "csv":
            return <FileSpreadsheet className={className} />;
        case "json":
            return <FileJson className={className} />;
        default:
            return <File className={className} />;
    }
};

export const shouldAutoRender = (filename?: string, mimeType?: string): boolean => {
    const renderType = getRenderType(filename, mimeType);
    return renderType === "image" || renderType === "audio" || renderType === "text" || renderType === "markdown";
};

export const isUserControllableRendering = (filename?: string, mimeType?: string): boolean => {
    const renderType = getRenderType(filename, mimeType);
    const controllableTypes = ["text", "markdown", "json", "yaml", "csv", "html", "image", "audio"];
    return renderType ? controllableTypes.includes(renderType) : false;
};

export const generateContentPreview = (content: string, maxLength: number = 200): string => {
    if (!content || typeof content !== "string") {
        return "";
    }

    const cleanedContent = content
        .replace(/\r\n/g, "\n")
        .replace(/\n{3,}/g, "\n\n")
        .replace(/[ \t]{2,}/g, " ")
        .trim();

    if (cleanedContent.length <= maxLength) {
        return cleanedContent;
    }

    const truncated = cleanedContent.substring(0, maxLength);
    const lastSpaceIndex = truncated.lastIndexOf(" ");
    const lastNewlineIndex = truncated.lastIndexOf("\n");

    const boundaryIndex = Math.max(lastSpaceIndex, lastNewlineIndex);
    if (boundaryIndex > maxLength * 0.7) {
        return truncated.substring(0, boundaryIndex) + "...";
    }

    return truncated + "...";
};

export const generateStructuredDataPreview = (content: string, type: "json" | "yaml", maxLength: number = 200): string => {
    try {
        if (type === "json") {
            const parsed = JSON.parse(content);
            if (typeof parsed === "object" && parsed !== null) {
                const keys = Object.keys(parsed).slice(0, 5);
                const preview = keys
                    .map(key => {
                        const value = parsed[key];
                        const valueType = Array.isArray(value) ? "array" : typeof value;
                        return `${key}: ${valueType}`;
                    })
                    .join("\n");
                return generateContentPreview(preview, maxLength);
            }
        } else if (type === "yaml") {
            const lines = content.split("\n").slice(0, 10);
            const keyValueLines = lines.filter(line => line.trim() && !line.trim().startsWith("#") && line.includes(":")).slice(0, 5);
            return generateContentPreview(keyValueLines.join("\n"), maxLength);
        }
    } catch (error) {
        console.warn(`Failed to parse ${type} for preview:`, error);
    }

    return generateContentPreview(content, maxLength);
};

export const extractPreviewTextForIcon = (content: string, filename?: string, mimeType?: string): string => {
    const renderType = getRenderType(filename, mimeType);

    switch (renderType) {
        case "json":
            return extractJsonPreview(content);
        case "yaml":
            return extractYamlPreview(content);
        case "csv":
            return extractCsvPreview(content);
        case "html":
            return extractHtmlPreview(content);
        case "markdown":
            return extractMarkdownPreview(content);
        default:
            return extractTextPreview(content);
    }
};

export const extractJsonPreview = (content: string): string => {
    try {
        const parsed = JSON.parse(content);
        if (typeof parsed === "object" && parsed !== null) {
            const keys = Object.keys(parsed).slice(0, 8);
            const preview = keys
                .map(key => {
                    const value = parsed[key];
                    if (typeof value === "string") {
                        return `"${key}": "${value.length > 20 ? value.substring(0, 20) + "..." : value}"`;
                    } else if (typeof value === "number" || typeof value === "boolean") {
                        return `"${key}": ${value}`;
                    } else if (Array.isArray(value)) {
                        return `"${key}": [${value.length} items]`;
                    } else if (typeof value === "object" && value !== null) {
                        return `"${key}": {...}`;
                    }
                    return `"${key}": ${typeof value}`;
                })
                .join("\n");
            return `{\n${preview}\n}`;
        }
        return JSON.stringify(parsed, null, 2).substring(0, 150);
    } catch {
        return content.substring(0, 150);
    }
};

export const extractYamlPreview = (content: string): string => {
    const lines = content.split("\n").slice(0, 12);
    const keyValueLines = lines
        .filter(line => {
            const trimmed = line.trim();
            return trimmed && !trimmed.startsWith("#") && (trimmed.includes(":") || trimmed.startsWith("-"));
        })
        .slice(0, 8);
    return keyValueLines.join("\n");
};

export const extractCsvPreview = (content: string): string => {
    const lines = content.split("\n").slice(0, 6);
    return lines
        .map(line => {
            return line.length > 50 ? line.substring(0, 50) + "..." : line;
        })
        .join("\n");
};

export const extractHtmlPreview = (content: string): string => {
    const structuralTags = content.match(/<(html|head|body|div|section|article|header|footer|nav|main|aside)[^>]*>/gi);
    if (structuralTags && structuralTags.length > 0) {
        return structuralTags.slice(0, 8).join("\n");
    }

    const textContent = DOMPurify.sanitize(content.replace(/<(?:p|div|h[1-6]|li|br|tr|td|th)[^>]*>/gi, " "), { ALLOWED_TAGS: [], ALLOWED_ATTR: [] })
        .replace(/\s+/g, " ")
        .trim();

    return textContent.substring(0, 150);
};

export const extractMarkdownPreview = (content: string): string => {
    const lines = content.split("\n").slice(0, 10);
    const meaningfulLines = lines
        .filter(line => {
            const trimmed = line.trim();
            return trimmed && !trimmed.startsWith("<!--");
        })
        .slice(0, 6);
    return meaningfulLines.join("\n");
};

export const extractTextPreview = (content: string): string => {
    const lines = content.split("\n").slice(0, 8);
    return lines.join("\n").substring(0, 150);
};

export const generateFileTypePreview = (content: string, filename?: string, mimeType?: string, maxLength: number = 200): string => {
    const renderType = getRenderType(filename, mimeType);

    switch (renderType) {
        case "json":
            return generateStructuredDataPreview(content, "json", maxLength);
        case "yaml":
            return generateStructuredDataPreview(content, "yaml", maxLength);
        case "csv": {
            const csvLines = content.split("\n").slice(0, 5);
            return generateContentPreview(csvLines.join("\n"), maxLength);
        }
        case "html": {
            const htmlPreview = DOMPurify.sanitize(content.replace(/<(?:p|div|h[1-6]|li|br|tr|td|th)[^>]*>/gi, " "), { ALLOWED_TAGS: [], ALLOWED_ATTR: [] })
                .replace(/\s+/g, " ")
                .trim();
            return generateContentPreview(htmlPreview || content, maxLength);
        }
        default:
            return generateContentPreview(content, maxLength);
    }
};
