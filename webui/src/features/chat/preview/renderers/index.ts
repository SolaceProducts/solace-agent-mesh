// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface BaseRendererProps {
    content: string;
    rendererType?: string;
    mimeType?: string;
    setRenderError: (error: string | null) => void;
    isStreaming?: boolean;
}

export { AudioRenderer } from "./AudioRenderer";
export { CsvRenderer } from "./CsvRenderer";
export { HtmlRenderer } from "./HTMLRenderer";
export { ImageRenderer } from "./ImageRenderer";
export { MarkdownRenderer } from "./MarkdownRenderer";
export { MermaidRenderer } from "./MermaidRenderer";
export { OoxmlDocumentRenderer } from "./OoxmlDocumentRenderer";
export { default as PdfRenderer, SNIP_TO_CHAT_EVENT, type SnipToChatEventDetail } from "./PdfRenderer";
export { StructuredDataRenderer } from "./StructuredDataRenderer";
export { TextRenderer } from "./TextRenderer";
