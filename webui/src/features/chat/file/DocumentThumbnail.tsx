// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";
import { Loader2 } from "lucide-react";
import pdfWorkerUrl from "pdfjs-dist/build/pdf.worker.min.mjs?url";
import { Document, Page, pdfjs } from "react-pdf";

import { useIsBinaryArtifactPreviewEnabled } from "@/hooks/useIsBinaryArtifactPreviewEnabled";

import { base64ToArrayBuffer, cn, createLruCache } from "@/utils";

import { MAX_ZIP_ENTRY_BYTES, withTimeout } from "../preview/renderers/withTimeout";

pdfjs.GlobalWorkerOptions.workerSrc = pdfWorkerUrl || "./pdf.worker.min.mjs";

interface DocumentThumbnailProps {
    readonly content: string;
    readonly filename: string;
    readonly mimeType?: string;
    readonly width?: number;
    readonly height?: number;
    readonly className?: string;
    readonly onError?: () => void;
    readonly onLoad?: () => void;
}

type OfficeKind = "docx" | "pptx" | "xlsx";

const officeThumbnailCache = createLruCache<string>(30);

function hashFullContent(content: string, filename: string): string {
    let hash = 0;
    for (let i = 0; i < content.length; i++) {
        hash = Math.imul(hash, 31) + (content.codePointAt(i) ?? 0);
    }
    return `thumb:${filename}:${content.length}:${hash}`;
}

function isPdf(filename: string, mimeType?: string): boolean {
    if (mimeType === "application/pdf") return true;
    return filename.toLowerCase().endsWith(".pdf");
}

function officeKind(filename: string, mimeType?: string): OfficeKind | null {
    const ext = filename.toLowerCase().split(".").pop();
    if (ext === "docx" || mimeType === "application/vnd.openxmlformats-officedocument.wordprocessingml.document") return "docx";
    if (ext === "pptx" || mimeType === "application/vnd.openxmlformats-officedocument.presentationml.presentation") return "pptx";
    if (ext === "xlsx" || mimeType === "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet") return "xlsx";
    return null;
}

export function supportsThumbnail(filename: string, mimeType?: string): boolean {
    return isPdf(filename, mimeType) || officeKind(filename, mimeType) !== null;
}

const pdfOptions = { withCredentials: true };

const OFFICE_RENDER_WIDTH = 320;

const THUMBNAIL_RENDER_TIMEOUT_MS = 30_000;

function bitmapToPngDataUrl(bmp: ImageBitmap): string {
    try {
        const canvas = document.createElement("canvas");
        canvas.width = bmp.width;
        canvas.height = bmp.height;
        const ctx = canvas.getContext("2d");
        if (!ctx) throw new Error("2D canvas context unavailable");
        ctx.drawImage(bmp, 0, 0);
        return canvas.toDataURL("image/png");
    } finally {
        bmp.close();
    }
}

async function renderOfficeThumbnail(kind: OfficeKind, bytes: ArrayBuffer): Promise<string> {
    if (kind === "docx") {
        const { DocxDocument } = await import("@silurus/ooxml/docx");
        const doc = await DocxDocument.load(bytes, { maxZipEntryBytes: MAX_ZIP_ENTRY_BYTES });
        try {
            return bitmapToPngDataUrl(await doc.renderPageToBitmap(0, { width: OFFICE_RENDER_WIDTH, dpr: 1 }));
        } finally {
            doc.destroy();
        }
    }
    if (kind === "pptx") {
        const { PptxPresentation } = await import("@silurus/ooxml/pptx");
        const pres = await PptxPresentation.load(bytes, { maxZipEntryBytes: MAX_ZIP_ENTRY_BYTES });
        try {
            return bitmapToPngDataUrl(await pres.renderSlideToBitmap(0, { width: OFFICE_RENDER_WIDTH, dpr: 1 }));
        } finally {
            pres.destroy();
        }
    }
    const { XlsxWorkbook } = await import("@silurus/ooxml/xlsx");
    const wb = await XlsxWorkbook.load(bytes, { maxZipEntryBytes: MAX_ZIP_ENTRY_BYTES });
    try {
        const bmp = await wb.renderViewportToBitmap(0, { row: 0, col: 0, rows: 16, cols: 6 }, { width: OFFICE_RENDER_WIDTH, height: Math.round(OFFICE_RENDER_WIDTH * 1.3), dpr: 1 });
        return bitmapToPngDataUrl(bmp);
    } finally {
        wb.destroy();
    }
}

export function DocumentThumbnail({ content, filename, mimeType, width = 52, height = 67, className, onError, onLoad }: DocumentThumbnailProps) {
    const [pdfDataUrl, setPdfDataUrl] = useState<string | null>(null);
    const [imgDataUrl, setImgDataUrl] = useState<string | null>(null);
    const [isLoading, setIsLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);
    const [pageLoaded, setPageLoaded] = useState(false);

    const onErrorRef = useRef(onError);
    const onLoadRef = useRef(onLoad);
    useEffect(() => {
        onErrorRef.current = onError;
        onLoadRef.current = onLoad;
    });

    const pdfErrorFiredRef = useRef(false);

    const binaryArtifactPreviewEnabled = useIsBinaryArtifactPreviewEnabled();

    useEffect(() => {
        let cancelled = false;
        pdfErrorFiredRef.current = false;

        const fail = (reason: string) => {
            if (cancelled) return;
            setError(reason);
            setIsLoading(false);
            onErrorRef.current?.();
        };

        const initialize = async () => {
            if (!content) {
                fail("No content provided");
                return;
            }
            setIsLoading(true);
            setError(null);

            if (isPdf(filename, mimeType)) {
                if (!cancelled) {
                    setPdfDataUrl(`data:application/pdf;base64,${content}`);
                    setIsLoading(false);
                }
                return;
            }

            const kind = officeKind(filename, mimeType);
            if (!kind) {
                fail("Unsupported format");
                return;
            }
            if (!binaryArtifactPreviewEnabled) {
                fail("Preview not enabled");
                return;
            }

            const cacheKey = hashFullContent(content, filename);
            const cached = officeThumbnailCache.get(cacheKey);
            if (cached) {
                if (!cancelled) {
                    setImgDataUrl(cached);
                    setIsLoading(false);
                    setPageLoaded(true);
                    onLoadRef.current?.();
                }
                return;
            }

            try {
                const dataUrl = await withTimeout(renderOfficeThumbnail(kind, base64ToArrayBuffer(content)), THUMBNAIL_RENDER_TIMEOUT_MS, "Thumbnail timed out while rendering.");
                if (cancelled) return;
                officeThumbnailCache.set(cacheKey, dataUrl);
                setImgDataUrl(dataUrl);
                setIsLoading(false);
                setPageLoaded(true);
                onLoadRef.current?.();
            } catch (err) {
                console.warn("Failed to render Office thumbnail:", err);
                fail("Conversion failed");
            }
        };

        initialize();

        return () => {
            cancelled = true;
        };
    }, [content, filename, mimeType, binaryArtifactPreviewEnabled]);

    const handlePageLoadSuccess = useCallback(() => {
        setPageLoaded(true);
        onLoadRef.current?.();
    }, []);

    const handlePageLoadError = useCallback(() => {
        if (pdfErrorFiredRef.current) return;
        pdfErrorFiredRef.current = true;
        setError("Failed to render");
        onErrorRef.current?.();
    }, []);

    const boxStyle = className?.includes("h-full") || className?.includes("w-full") ? undefined : { width, height };

    if (isLoading) {
        return (
            <div className={cn("flex items-center justify-center bg-(--secondary-w10)", className)} style={boxStyle}>
                <Loader2 className="h-4 w-4 animate-spin text-(--secondary-text-wMain)" />
            </div>
        );
    }

    if (error || (!pdfDataUrl && !imgDataUrl)) {
        return null;
    }

    if (imgDataUrl) {
        return (
            <div className={cn("relative overflow-hidden bg-(--lightSurface-bgActive)", className)} style={boxStyle}>
                <img src={imgDataUrl} alt="" className="h-full w-full object-cover object-top" />
            </div>
        );
    }

    const scale = 0.5;
    return (
        <div className={cn("relative overflow-hidden bg-(--lightSurface-bgActive)", className)} style={boxStyle}>
            {!pageLoaded && (
                <div className="absolute inset-0 flex items-center justify-center bg-(--secondary-w10)">
                    <Loader2 className="h-3 w-3 animate-spin text-(--secondary-text-wMain)" />
                </div>
            )}
            <div className={cn("h-full w-full transition-opacity duration-200", !pageLoaded && "opacity-0")}>
                <Document file={pdfDataUrl} options={pdfOptions} loading={null} error={null} onLoadError={handlePageLoadError} className="thumbnail-document">
                    <Page pageNumber={1} scale={scale} renderTextLayer={false} renderAnnotationLayer={false} onLoadSuccess={handlePageLoadSuccess} onLoadError={handlePageLoadError} className="thumbnail-page" />
                </Document>
            </div>
        </div>
    );
}

export default DocumentThumbnail;
