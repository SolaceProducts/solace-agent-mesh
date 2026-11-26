// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { RAGSearchResult } from "@/types";

import { AudioRenderer, CsvRenderer, HtmlRenderer, ImageRenderer, MarkdownRenderer, MermaidRenderer, OoxmlDocumentRenderer, PdfRenderer, StructuredDataRenderer, TextRenderer } from "./renderers";
import type { CitationMapEntry } from "./renderers/PdfRenderer";

interface ContentRendererProps {
    content: string;
    rendererType: string;
    mimeType?: string;
    url?: string;
    filename?: string;
    setRenderError: (error: string | null) => void;
    isStreaming?: boolean;
    ragData?: RAGSearchResult;
    initialPage?: number;
    citationMaps?: CitationMapEntry[];
    disableInteractionModes?: boolean;
}

export function ContentRenderer({ content, rendererType, mimeType, url, filename, setRenderError, isStreaming, ragData, initialPage, citationMaps, disableInteractionModes }: ContentRendererProps) {
    switch (rendererType) {
        case "csv":
            return <CsvRenderer content={content} setRenderError={setRenderError} />;
        case "mermaid":
            return <MermaidRenderer content={content} setRenderError={setRenderError} isStreaming={isStreaming} />;
        case "html":
            return <HtmlRenderer content={content} setRenderError={setRenderError} />;
        case "json":
        case "yaml":
            return <StructuredDataRenderer content={content} rendererType={rendererType} setRenderError={setRenderError} isStreaming={isStreaming} />;
        case "image":
            return <ImageRenderer content={content} mimeType={mimeType} setRenderError={setRenderError} />;
        case "markdown":
            return <MarkdownRenderer content={content} setRenderError={setRenderError} isStreaming={isStreaming} ragData={ragData} />;
        case "audio":
            return <AudioRenderer content={content} mimeType={mimeType} setRenderError={setRenderError} />;
        case "docx":
            return <OoxmlDocumentRenderer content={content} filename={filename || "document.docx"} documentType="docx" setRenderError={setRenderError} />;
        case "pptx":
            return <OoxmlDocumentRenderer content={content} filename={filename || "presentation.pptx"} documentType="pptx" setRenderError={setRenderError} />;
        case "xlsx":
            return <OoxmlDocumentRenderer content={content} filename={filename || "spreadsheet.xlsx"} documentType="xlsx" setRenderError={setRenderError} />;
        case "pdf":
        case "application/pdf":
            if (url && filename) {
                return <PdfRenderer url={url} filename={filename} initialPage={initialPage} citationMaps={citationMaps} disableInteractionModes={disableInteractionModes} />;
            }
            setRenderError("URL and filename are required for PDF preview.");
            return null;
        default:
            return <TextRenderer content={content} setRenderError={setRenderError} isStreaming={isStreaming} />;
    }
}
