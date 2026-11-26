// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { formatBytes } from "@/utils/format";

import type { ArtifactInfo, FileAttachment } from "@/types";

function isTextFile(fileName?: string, mimeType?: string): boolean {
    if (mimeType) {
        const lowerMime = mimeType.toLowerCase();
        if (lowerMime.startsWith("text/")) {
            return true;
        }
    }
    if (!fileName) return false;

    const lowerFileName = fileName.toLowerCase();

    if (lowerFileName.endsWith(".txt") || lowerFileName.endsWith(".text")) return true;

    if (lowerFileName.endsWith(".sql")) return true;

    if (lowerFileName.endsWith(".xml")) return true;
    if (lowerFileName.endsWith(".toml")) return true;
    if (lowerFileName.endsWith(".ini")) return true;
    if (lowerFileName.endsWith(".conf") || lowerFileName.endsWith(".config")) return true;
    if (lowerFileName.endsWith(".properties")) return true;

    if (lowerFileName.endsWith(".py")) return true;
    if (lowerFileName.endsWith(".js") || lowerFileName.endsWith(".ts") || lowerFileName.endsWith(".jsx") || lowerFileName.endsWith(".tsx")) return true;
    if (lowerFileName.endsWith(".java")) return true;
    if (lowerFileName.endsWith(".c") || lowerFileName.endsWith(".cpp") || lowerFileName.endsWith(".h") || lowerFileName.endsWith(".hpp")) return true;
    if (lowerFileName.endsWith(".cs")) return true;
    if (lowerFileName.endsWith(".go")) return true;
    if (lowerFileName.endsWith(".rs")) return true;
    if (lowerFileName.endsWith(".rb")) return true;
    if (lowerFileName.endsWith(".php")) return true;
    if (lowerFileName.endsWith(".swift")) return true;
    if (lowerFileName.endsWith(".kt")) return true;
    if (lowerFileName.endsWith(".scala")) return true;

    if (lowerFileName.endsWith(".sh")) return true;
    if (lowerFileName.endsWith(".bash")) return true;
    if (lowerFileName.endsWith(".zsh")) return true;
    if (lowerFileName.endsWith(".bat") || lowerFileName.endsWith(".cmd")) return true;
    if (lowerFileName.endsWith(".ps1")) return true;

    if (lowerFileName.endsWith(".css")) return true;
    if (lowerFileName.endsWith(".scss") || lowerFileName.endsWith(".sass") || lowerFileName.endsWith(".less")) return true;

    if (lowerFileName.endsWith(".log")) return true;
    if (lowerFileName.endsWith(".env")) return true;
    if (lowerFileName.endsWith(".gitignore") || lowerFileName.endsWith(".dockerignore")) return true;
    if (lowerFileName.endsWith(".editorconfig")) return true;

    return false;
}

function isHtmlFile(fileName?: string, mimeType?: string): boolean {
    if (mimeType) {
        const lowerMime = mimeType.toLowerCase();
        if (lowerMime === "text/html" || lowerMime === "application/xhtml+xml") {
            return true;
        }
    }
    if (!fileName) return false;
    return fileName.toLowerCase().endsWith(".html") || fileName.toLowerCase().endsWith(".htm");
}

function isMermaidFile(fileName?: string, mimeType?: string): boolean {
    if (mimeType) {
        const lowerMime = mimeType.toLowerCase();
        if (lowerMime === "text/x-mermaid" || lowerMime === "application/x-mermaid") {
            return true;
        }
    }
    if (!fileName) return false;
    return fileName.toLowerCase().endsWith(".mermaid") || fileName.toLowerCase().endsWith(".mmd");
}

function isCsvFile(fileName?: string, mimeType?: string): boolean {
    if (mimeType) {
        const lowerMime = mimeType.toLowerCase();
        if (lowerMime === "text/csv" || lowerMime === "application/csv") {
            return true;
        }
    }
    if (!fileName) return false;
    return fileName.toLowerCase().endsWith(".csv");
}

function isImageFile(fileName?: string, mimeType?: string): boolean {
    if (mimeType) {
        const lowerMime = mimeType.toLowerCase();
        if (lowerMime.startsWith("image/")) {
            return true;
        }
        if (lowerMime.startsWith("text/") || lowerMime.startsWith("application/")) {
            return false;
        }
    }
    if (!fileName) return false;
    const lowerCaseFileName = fileName.toLowerCase();
    return (
        lowerCaseFileName.endsWith(".png") ||
        lowerCaseFileName.endsWith(".jpg") ||
        lowerCaseFileName.endsWith(".jpeg") ||
        lowerCaseFileName.endsWith(".gif") ||
        lowerCaseFileName.endsWith(".bmp") ||
        lowerCaseFileName.endsWith(".webp") ||
        lowerCaseFileName.endsWith(".svg")
    );
}

function isJsonFile(fileName?: string, mimeType?: string): boolean {
    if (mimeType) {
        const lowerMime = mimeType.toLowerCase();
        if (lowerMime === "application/json" || lowerMime === "text/json") {
            return true;
        }
    }
    if (!fileName) return false;
    return fileName.toLowerCase().endsWith(".json");
}

function isYamlFile(fileName?: string, mimeType?: string): boolean {
    if (mimeType) {
        const lowerMime = mimeType.toLowerCase();
        if (lowerMime === "application/yaml" || lowerMime === "text/yaml" || lowerMime === "application/x-yaml" || lowerMime === "text/x-yaml") {
            return true;
        }
    }
    if (!fileName) return false;
    const lowerFileName = fileName.toLowerCase();
    return lowerFileName.endsWith(".yaml") || lowerFileName.endsWith(".yml");
}

function isMarkdownFile(fileName?: string, mimeType?: string): boolean {
    if (mimeType) {
        const lowerMime = mimeType.toLowerCase();
        if (lowerMime === "text/markdown" || lowerMime === "application/markdown" || lowerMime === "text/x-markdown") {
            return true;
        }
    }
    if (!fileName) return false;
    const lowerCaseFileName = fileName.toLowerCase();
    return lowerCaseFileName.endsWith(".md") || lowerCaseFileName.endsWith(".markdown");
}

function isAudioFile(fileName?: string, mimeType?: string): boolean {
    if (mimeType) {
        const lowerMime = mimeType.toLowerCase();
        if (lowerMime.startsWith("audio/")) {
            return true;
        }

        if (lowerMime.startsWith("text/") || lowerMime.startsWith("application/") || lowerMime.startsWith("image/")) {
            return false;
        }
    }
    if (!fileName) return false;
    const lowerCaseFileName = fileName.toLowerCase();
    return lowerCaseFileName.endsWith(".mp3") || lowerCaseFileName.endsWith(".wav") || lowerCaseFileName.endsWith(".ogg") || lowerCaseFileName.endsWith(".aac") || lowerCaseFileName.endsWith(".flac") || lowerCaseFileName.endsWith(".m4a");
}

function isDocxFile(fileName?: string, mimeType?: string): boolean {
    if (mimeType) {
        const lowerMime = mimeType.toLowerCase();
        if (lowerMime === "application/vnd.openxmlformats-officedocument.wordprocessingml.document") {
            return true;
        }
    }
    if (!fileName) return false;
    return fileName.toLowerCase().endsWith(".docx");
}

function isPdfFile(fileName?: string, mimeType?: string): boolean {
    if (mimeType) {
        const lowerMime = mimeType.toLowerCase();
        if (lowerMime === "application/pdf") {
            return true;
        }
    }
    if (!fileName) return false;
    return fileName.toLowerCase().endsWith(".pdf");
}

function isPptxFile(fileName?: string, mimeType?: string): boolean {
    if (mimeType) {
        const lowerMime = mimeType.toLowerCase();
        if (lowerMime === "application/vnd.openxmlformats-officedocument.presentationml.presentation") {
            return true;
        }
    }
    if (!fileName) return false;
    return fileName.toLowerCase().endsWith(".pptx");
}

function isXlsxFile(fileName?: string, mimeType?: string): boolean {
    if (mimeType) {
        const lowerMime = mimeType.toLowerCase();
        if (lowerMime === "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet") {
            return true;
        }
    }
    if (!fileName) return false;
    return fileName.toLowerCase().endsWith(".xlsx");
}

export function getRenderType(fileName?: string, mimeType?: string): string | null {
    if (isHtmlFile(fileName, mimeType)) {
        return "html";
    }

    if (isMermaidFile(fileName, mimeType)) {
        return "mermaid";
    }

    if (isImageFile(fileName, mimeType)) {
        return "image";
    }

    if (isMarkdownFile(fileName, mimeType)) {
        return "markdown";
    }

    if (isAudioFile(fileName, mimeType)) {
        return "audio";
    }

    if (isJsonFile(fileName, mimeType)) {
        return "json";
    }

    if (isYamlFile(fileName, mimeType)) {
        return "yaml";
    }

    if (isCsvFile(fileName, mimeType)) {
        return "csv";
    }

    if (isDocxFile(fileName, mimeType)) {
        return "docx";
    }

    if (isPptxFile(fileName, mimeType)) {
        return "pptx";
    }

    if (isXlsxFile(fileName, mimeType)) {
        return "xlsx";
    }

    if (isPdfFile(fileName, mimeType)) {
        return "pdf";
    }

    if (isTextFile(fileName, mimeType)) {
        return "text";
    }

    return null;
}

export function encodeBase64Content(text: string): string {
    const encoder = new TextEncoder();
    const bytes = encoder.encode(text);
    let binary = "";
    for (let i = 0; i < bytes.length; i++) {
        binary += String.fromCharCode(bytes[i]);
    }
    return btoa(binary);
}

function isValidBase64(str: string): boolean {
    if (!str || typeof str !== "string") {
        return false;
    }
    const base64Regex = /^[A-Za-z0-9+/]*={0,2}$/;
    const trimmed = str.trim();
    return base64Regex.test(trimmed) && trimmed.length % 4 === 0;
}

export function decodeBase64Content(content: string): string {
    if (!content || typeof content !== "string") {
        return content || "";
    }

    if (!isValidBase64(content)) {
        return content;
    }

    try {
        const bytes = Uint8Array.from(atob(content), c => c.charCodeAt(0));
        return new TextDecoder("utf-8", { fatal: false }).decode(bytes);
    } catch (error) {
        console.debug("TextDecoder failed (potentially non-UTF8 data), falling back to simple atob:", error);
        try {
            return atob(content);
        } catch (atobError) {
            console.debug("Failed to decode base64 content with atob fallback, returning original content:", atobError);
            return content;
        }
    }
}

const RENDER_TYPES = ["csv", "html", "json", "mermaid", "image", "markdown", "audio", "text", "yaml", "docx", "pptx", "xlsx", "pdf"];
export const RENDER_TYPES_WITH_RAW_CONTENT = ["image", "audio", "docx", "pptx", "xlsx"];
export const RENDER_TYPES_WITH_URL_ONLY = ["pdf"];

export const getFileContent = (file: FileAttachment | null) => {
    if (!file) {
        return "";
    }

    const renderType = getRenderType(file.name, file.mimeType);

    if (!renderType || !RENDER_TYPES.includes(renderType)) {
        return "";
    }

    if (RENDER_TYPES_WITH_URL_ONLY.includes(renderType)) {
        return "url-based-content";
    }

    if (!file.content) {
        return "";
    }

    if (RENDER_TYPES_WITH_RAW_CONTENT.includes(renderType)) {
        return file.content;
    }

    // @ts-expect-error - Custom property added during streaming
    if (file.isPlainText) {
        return file.content;
    }

    try {
        return decodeBase64Content(file.content);
    } catch (e) {
        console.error("Failed to decode base64 content:", e);
        return "";
    }
};

const MAX_ARTIFACT_SIZE = 5 * 1024 * 1024;
const MAX_ARTIFACT_SIZE_URL_BASED = 50 * 1024 * 1024;
const MAX_ARTIFACT_SIZE_HUMAN = formatBytes(MAX_ARTIFACT_SIZE);
const MAX_ARTIFACT_SIZE_URL_BASED_HUMAN = formatBytes(MAX_ARTIFACT_SIZE_URL_BASED);

export function canPreviewArtifact(artifact: ArtifactInfo | null): { canPreview: boolean; reason?: string } {
    if (!artifact || !artifact.size) {
        return { canPreview: false, reason: "No artifact or content available." };
    }

    const renderType = getRenderType(artifact.filename, artifact.mimeType);
    if (!renderType || !RENDER_TYPES.includes(renderType)) {
        return { canPreview: false, reason: "Preview not yet supported for this file type." };
    }

    const isUrlBasedRenderer = RENDER_TYPES_WITH_URL_ONLY.includes(renderType);
    const maxSize = isUrlBasedRenderer ? MAX_ARTIFACT_SIZE_URL_BASED : MAX_ARTIFACT_SIZE;
    const maxSizeHuman = isUrlBasedRenderer ? MAX_ARTIFACT_SIZE_URL_BASED_HUMAN : MAX_ARTIFACT_SIZE_HUMAN;

    if (artifact.size > maxSize) {
        return {
            canPreview: false,
            reason: `Preview not supported for files this large. Maximum size is: ${maxSizeHuman}.`,
        };
    }

    return { canPreview: true };
}
