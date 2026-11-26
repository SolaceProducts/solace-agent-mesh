// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import "react-pdf/dist/esm/Page/AnnotationLayer.css";
import "react-pdf/dist/esm/Page/TextLayer.css";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Hand, ScanLine, Scissors, ZoomIn, ZoomOut } from "lucide-react";
import pdfWorkerUrl from "pdfjs-dist/build/pdf.worker.min.mjs?url";
import { Document, Page, pdfjs } from "react-pdf";

import { usePdfBlob } from "@/api/artifacts/hooks";

import { scrollToElement } from "@/hooks/useScrollToHighlight";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";

const MIN_ZOOM = 0.2;

export const SNIP_TO_CHAT_EVENT = "snip-to-chat";

export interface SnipToChatEventDetail {
    file: File;
    filename: string;
}

pdfjs.GlobalWorkerOptions.workerSrc = pdfWorkerUrl || "./pdf.worker.min.mjs";

export interface CitationMapEntry {
    location: string;
    char_start: number;
    char_end: number;
}

interface PdfRendererProps {
    url: string;
    filename: string;
    initialPage?: number;
    citationMaps?: CitationMapEntry[];
    disableInteractionModes?: boolean;
}

interface SelectionRect {
    startX: number;
    startY: number;
    endX: number;
    endY: number;
}

type InteractionMode = "text" | "pan" | "snip";

const PdfRenderer: React.FC<PdfRendererProps> = ({ url, filename, initialPage, citationMaps = [], disableInteractionModes = false }) => {
    const { data: resolvedUrl, error: fetchError } = usePdfBlob(url);

    const pdfOptions = useMemo(() => ({ withCredentials: false }), []);
    const [numPages, setNumPages] = useState<number | null>(null);
    const [error, setError] = useState<string | null>(null);
    const [zoomLevel, setZoomLevel] = useState(1);
    const [pan, setPan] = useState({ x: 0, y: 0 });
    const [isDragging, setIsDragging] = useState(false);
    const [dragStart, setDragStart] = useState({ x: 0, y: 0 });
    const [pageWidth, setPageWidth] = useState<number | null>(null);
    const [interactionMode, setInteractionMode] = useState<InteractionMode>("text");
    const [selection, setSelection] = useState<SelectionRect | null>(null);
    const [isSelecting, setIsSelecting] = useState(false);
    const [snipStatus, setSnipStatus] = useState<"idle" | "processing" | "success" | "error">("idle");
    const [pageCharBoundaries, setPageCharBoundaries] = useState<number[]>([]);
    const [isWaitingForHighlight, setIsWaitingForHighlight] = useState(citationMaps.length > 0);
    const viewerRef = useRef<HTMLDivElement>(null);
    const documentContainerRef = useRef<HTMLDivElement>(null);
    const pageRefs = useRef<Map<number, HTMLDivElement>>(new Map());

    const userZoomedRef = useRef(false);

    useEffect(() => {
        const viewer = viewerRef.current;
        if (!pageWidth || !viewer) return;
        const applyInitialFit = () => {
            if (userZoomedRef.current) return;
            setZoomLevel(Math.max(((viewer.clientWidth - 40) / pageWidth) * 0.5, MIN_ZOOM));
            setPan({ x: 0, y: 0 });
        };
        applyInitialFit();
        const observer = new ResizeObserver(applyInitialFit);
        observer.observe(viewer);
        return () => observer.disconnect();
    }, [pageWidth]);

    useEffect(() => {
        const hasHighlighting = citationMaps.length > 0;
        if (initialPage && initialPage > 0 && numPages && initialPage <= numPages && !hasHighlighting) {
            requestAnimationFrame(() => {
                const pageElement = pageRefs.current.get(initialPage);
                if (pageElement && viewerRef.current) {
                    pageElement.scrollIntoView({ behavior: "smooth", block: "start" });
                }
            });
        }
    }, [initialPage, numPages, citationMaps.length]);

    useEffect(() => {
        if (!citationMaps.length || !numPages || !resolvedUrl) return;

        let cancelled = false;
        const loadingTask = pdfjs.getDocument(resolvedUrl);

        const buildPageBoundaries = async () => {
            try {
                const pdf = await loadingTask.promise;
                if (cancelled) return;

                let charCount = 0;
                const boundaries: number[] = [0];

                for (let i = 1; i <= pdf.numPages; i++) {
                    if (cancelled) return;
                    const page = await pdf.getPage(i);
                    const content = await page.getTextContent();

                    for (const item of content.items) {
                        if ("str" in item) {
                            charCount += item.str.length;
                        }
                    }
                    boundaries.push(charCount);
                }

                if (!cancelled) {
                    setPageCharBoundaries(boundaries);
                }
            } catch (err) {
                if (!cancelled && !(err instanceof Error && err.name === "AbortException")) {
                    console.error("[PdfRenderer] Failed to build page boundaries (char-position highlighting disabled):", err);
                }
            }
        };

        buildPageBoundaries();

        return () => {
            cancelled = true;
            loadingTask.destroy();
        };
    }, [citationMaps, numPages, resolvedUrl]);

    useEffect(() => {
        if (!numPages || !viewerRef.current) return;

        const hasCitationMaps = citationMaps.length > 0 && pageCharBoundaries.length > 0;

        if (!hasCitationMaps) return;

        let cancelled = false;
        let attempts = 0;
        const maxAttempts = 50;

        const waitForTextLayerAndHighlight = () => {
            if (cancelled) return;

            attempts++;

            const pagesWithCitations: number[] = [];
            for (let pageNum = 1; pageNum <= numPages; pageNum++) {
                const pageStart = pageCharBoundaries[pageNum - 1] || 0;
                const pageEnd = pageCharBoundaries[pageNum] || pageStart;
                const relevantMaps = citationMaps.filter(m => m.char_start < pageEnd && m.char_end > pageStart);
                if (relevantMaps.length > 0) {
                    pagesWithCitations.push(pageNum);
                }
            }

            if (pagesWithCitations.length === 0) {
                setIsWaitingForHighlight(false);
                return;
            }

            let totalSpansFound = 0;
            for (const pageNum of pagesWithCitations) {
                const pageElement = pageRefs.current.get(pageNum);
                if (pageElement) {
                    const textSpans = pageElement.querySelectorAll(".react-pdf__Page__textContent span");
                    totalSpansFound += textSpans.length;
                }
            }

            if (totalSpansFound === 0) {
                if (attempts < maxAttempts) {
                    setTimeout(waitForTextLayerAndHighlight, 100);
                    return;
                } else {
                    setIsWaitingForHighlight(false);
                    return;
                }
            }

            const matchedSpans: HTMLElement[] = [];

            for (let pageNum = 1; pageNum <= numPages; pageNum++) {
                const pageElement = pageRefs.current.get(pageNum);
                if (!pageElement) continue;

                const pageStart = pageCharBoundaries[pageNum - 1] || 0;
                const pageEnd = pageCharBoundaries[pageNum] || pageStart;

                const relevantMaps = citationMaps.filter(m => m.char_start < pageEnd && m.char_end > pageStart);

                if (relevantMaps.length === 0) continue;

                const textSpans = pageElement.querySelectorAll(".react-pdf__Page__textContent span");
                let charPos = pageStart;

                textSpans.forEach(span => {
                    const spanText = span.textContent || "";
                    const spanStart = charPos;
                    const spanEnd = charPos + spanText.length;

                    const overlaps = relevantMaps.some(m => spanStart < m.char_end && spanEnd > m.char_start);

                    if (overlaps && spanText.trim().length > 0) {
                        span.classList.add("citation-highlight");
                        matchedSpans.push(span as HTMLElement);
                    }

                    charPos = spanEnd;
                });
            }

            if (matchedSpans.length > 0) {
                scrollToElement(matchedSpans[0], () => {
                    setIsWaitingForHighlight(false);
                });
            } else {
                setIsWaitingForHighlight(false);
            }
        };

        waitForTextLayerAndHighlight();

        return () => {
            cancelled = true;
        };
    }, [citationMaps, pageCharBoundaries, numPages, interactionMode]);

    function onDocumentLoadSuccess({ numPages: nextNumPages }: { numPages: number }): void {
        setNumPages(nextNumPages);
        setError(null);
    }

    function onDocumentLoadError(error: Error): void {
        console.error("PDF load error:", error);
        let errorMessage = "Failed to load PDF. Please try downloading the file instead.";
        if (error.message?.includes("Invalid PDF structure")) {
            errorMessage = "This PDF file appears to be corrupted or has an invalid structure.";
        } else if (error.message?.includes("API version")) {
            errorMessage = "PDF viewer version mismatch. Please refresh the page.";
        } else if (error.message?.includes("Loading")) {
            errorMessage = "Unable to load the PDF file due to network issues or file corruption.";
        }
        setError(errorMessage);
    }

    const zoomIn = () => {
        userZoomedRef.current = true;
        setZoomLevel(prev => Math.min(prev + 0.2, 3));
    };
    const zoomOut = () => {
        userZoomedRef.current = true;
        setZoomLevel(prev => Math.max(prev - 0.2, MIN_ZOOM));
    };

    const fitToPage = useCallback(() => {
        userZoomedRef.current = true;
        if (viewerRef.current && pageWidth) {
            const containerWidth = viewerRef.current.clientWidth;
            const scale = (containerWidth - 40) / pageWidth;
            setZoomLevel(scale);
            setPan({ x: 0, y: 0 });
        }
    }, [pageWidth]);

    const handleMouseDown = (e: React.MouseEvent) => {
        if (e.button !== 0) return;

        if (interactionMode === "pan") {
            setIsDragging(true);
            setDragStart({ x: e.clientX - pan.x, y: e.clientY - pan.y });
        } else if (interactionMode === "snip" && viewerRef.current) {
            const rect = viewerRef.current.getBoundingClientRect();
            const x = e.clientX - rect.left + viewerRef.current.scrollLeft;
            const y = e.clientY - rect.top + viewerRef.current.scrollTop;
            setSelection({ startX: x, startY: y, endX: x, endY: y });
            setIsSelecting(true);
        }
    };

    const handleMouseMove = (e: React.MouseEvent) => {
        if (interactionMode === "pan" && isDragging) {
            setPan({ x: e.clientX - dragStart.x, y: e.clientY - dragStart.y });
        } else if (interactionMode === "snip" && isSelecting && viewerRef.current && selection) {
            const rect = viewerRef.current.getBoundingClientRect();
            const x = e.clientX - rect.left + viewerRef.current.scrollLeft;
            const y = e.clientY - rect.top + viewerRef.current.scrollTop;
            setSelection({ ...selection, endX: x, endY: y });
        }
    };

    const handleMouseUp = () => {
        if (interactionMode === "pan") {
            setIsDragging(false);
        } else if (interactionMode === "snip" && isSelecting) {
            setIsSelecting(false);
            if (selection) {
                const selWidth = Math.abs(selection.endX - selection.startX);
                const selHeight = Math.abs(selection.endY - selection.startY);
                if (selWidth >= 10 && selHeight >= 10) {
                    captureSnip();
                }
            }
        }
    };

    const captureSnip = async () => {
        if (!selection || !viewerRef.current) return;

        setSnipStatus("processing");

        try {
            const selX = Math.min(selection.startX, selection.endX);
            const selY = Math.min(selection.startY, selection.endY);
            const selWidth = Math.abs(selection.endX - selection.startX);
            const selHeight = Math.abs(selection.endY - selection.startY);

            const canvases = viewerRef.current.querySelectorAll("canvas");
            if (canvases.length === 0) {
                setSnipStatus("error");
                setTimeout(() => setSnipStatus("idle"), 2000);
                return;
            }

            const outputCanvas = document.createElement("canvas");
            outputCanvas.width = selWidth;
            outputCanvas.height = selHeight;
            const ctx = outputCanvas.getContext("2d");

            if (!ctx) {
                setSnipStatus("error");
                setTimeout(() => setSnipStatus("idle"), 2000);
                return;
            }

            ctx.fillStyle = "white";
            ctx.fillRect(0, 0, selWidth, selHeight);

            const viewerRect = viewerRef.current.getBoundingClientRect();
            const scrollLeft = viewerRef.current.scrollLeft;
            const scrollTop = viewerRef.current.scrollTop;

            canvases.forEach(sourceCanvas => {
                const canvasRect = sourceCanvas.getBoundingClientRect();

                const canvasScrollX = canvasRect.left - viewerRect.left + scrollLeft;
                const canvasScrollY = canvasRect.top - viewerRect.top + scrollTop;

                if (canvasScrollX + canvasRect.width <= selX || canvasScrollX >= selX + selWidth || canvasScrollY + canvasRect.height <= selY || canvasScrollY >= selY + selHeight) {
                    return;
                }

                const overlapX1 = Math.max(selX, canvasScrollX);
                const overlapY1 = Math.max(selY, canvasScrollY);
                const overlapX2 = Math.min(selX + selWidth, canvasScrollX + canvasRect.width);
                const overlapY2 = Math.min(selY + selHeight, canvasScrollY + canvasRect.height);

                const ratioX = sourceCanvas.width / canvasRect.width;
                const ratioY = sourceCanvas.height / canvasRect.height;

                const srcX = (overlapX1 - canvasScrollX) * ratioX;
                const srcY = (overlapY1 - canvasScrollY) * ratioY;
                const srcW = (overlapX2 - overlapX1) * ratioX;
                const srcH = (overlapY2 - overlapY1) * ratioY;

                const destX = overlapX1 - selX;
                const destY = overlapY1 - selY;
                const destW = overlapX2 - overlapX1;
                const destH = overlapY2 - overlapY1;

                ctx.drawImage(sourceCanvas, srcX, srcY, srcW, srcH, destX, destY, destW, destH);
            });

            const dataUrl = outputCanvas.toDataURL("image/png");

            const arr = dataUrl.split(",");
            const mime = arr[0].match(/:(.*?);/)?.[1] || "image/png";
            const bstr = atob(arr[1]);
            let n = bstr.length;
            const u8arr = new Uint8Array(n);
            while (n--) {
                u8arr[n] = bstr.charCodeAt(n);
            }
            const blob = new Blob([u8arr], { type: mime });

            sendToChat(blob);
        } catch (err) {
            console.error("Error capturing selection:", err);
            setSnipStatus("error");
            setTimeout(() => setSnipStatus("idle"), 2000);
        }
    };

    const sendToChat = (blob: Blob) => {
        if (!blob) return;

        const snipFilename = `${filename.replace(/\.[^/.]+$/, "")}-snip.png`;
        const file = new File([blob], snipFilename, { type: "image/png" });

        const event = new CustomEvent<SnipToChatEventDetail>(SNIP_TO_CHAT_EVENT, {
            detail: { file, filename: snipFilename },
            bubbles: true,
        });
        window.dispatchEvent(event);

        setSnipStatus("success");
        setTimeout(() => {
            setSnipStatus("idle");
            setSelection(null);
        }, 1500);
    };

    const setMode = (mode: InteractionMode) => {
        setInteractionMode(mode);
        setSelection(null);
        setSnipStatus("idle");
    };

    const handleWheel = (e: React.WheelEvent) => {
        if (e.ctrlKey || e.metaKey) {
            e.preventDefault();
            if (e.deltaY < 0) {
                zoomIn();
            } else {
                zoomOut();
            }
        }
    };

    const getSelectionStyle = (): React.CSSProperties | null => {
        if (!selection) return null;

        const x = Math.min(selection.startX, selection.endX);
        const y = Math.min(selection.startY, selection.endY);
        const width = Math.abs(selection.endX - selection.startX);
        const height = Math.abs(selection.endY - selection.startY);

        return {
            position: "absolute",
            left: x,
            top: y,
            width,
            height,
            border: "2px dashed #3b82f6",
            backgroundColor: "rgba(59, 130, 246, 0.1)",
            pointerEvents: "none",
            zIndex: 10,
        };
    };

    const getCursor = (): string => {
        if (interactionMode === "pan") {
            return isDragging ? "grabbing" : "grab";
        } else if (interactionMode === "snip") {
            return "crosshair";
        }
        return "auto";
    };

    if (fetchError) {
        return (
            <div className="flex h-full flex-col overflow-auto p-4">
                <div className="flex flex-grow flex-col items-center justify-center text-center">
                    <div className="mb-4 p-4 text-(--error-wMain)">{fetchError instanceof Error ? fetchError.message : "Failed to load PDF."}</div>
                    <a href={url} download={filename} target="_blank" rel="noopener noreferrer" className="text-(--info-wMain) hover:underline">
                        Download PDF
                    </a>
                </div>
            </div>
        );
    }

    if (error) {
        return (
            <div className="flex h-full flex-col overflow-auto p-4">
                <div className="flex flex-grow flex-col items-center justify-center text-center">
                    <div className="mb-4 p-4 text-(--error-wMain)">{error}</div>
                    <a href={url} download={filename} target="_blank" rel="noopener noreferrer" className="text-(--info-wMain) hover:underline">
                        Download PDF
                    </a>
                </div>
            </div>
        );
    }

    if (!resolvedUrl) {
        return (
            <div className="flex h-full flex-col overflow-auto p-4">
                <div className="flex flex-grow flex-col items-center justify-center text-center">
                    <div className="p-4 text-(--secondary-text-wMain)">Loading PDF...</div>
                </div>
            </div>
        );
    }

    return (
        <div className="flex h-full flex-col bg-(--secondary-w10) p-4">
            <div className="mb-2 flex items-center justify-center">
                <div className="flex items-center gap-2 rounded-lg bg-(--background-w10)/80 px-3 py-1.5 shadow-sm backdrop-blur-sm">
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <button onClick={zoomOut} className="rounded p-1 hover:bg-(--secondary-w20)">
                                <ZoomOut className="h-4 w-4" />
                            </button>
                        </TooltipTrigger>
                        <TooltipContent>Zoom Out</TooltipContent>
                    </Tooltip>
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <button onClick={zoomIn} className="rounded p-1 hover:bg-(--secondary-w20)">
                                <ZoomIn className="h-4 w-4" />
                            </button>
                        </TooltipTrigger>
                        <TooltipContent>Zoom In</TooltipContent>
                    </Tooltip>
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <button onClick={fitToPage} className="rounded p-1 hover:bg-(--secondary-w20)">
                                <ScanLine className="h-4 w-4" />
                            </button>
                        </TooltipTrigger>
                        <TooltipContent>Fit to Width</TooltipContent>
                    </Tooltip>
                    {!disableInteractionModes && (
                        <>
                            <div className="mx-1 h-4 w-px bg-(--secondary-w40)" />
                            <Tooltip>
                                <TooltipTrigger asChild>
                                    <button onClick={() => setMode(interactionMode === "pan" ? "text" : "pan")} className={`rounded p-1 ${interactionMode === "pan" ? "bg-(--info-w10) text-(--info-wMain)" : "hover:bg-(--secondary-w20)"}`}>
                                        <Hand className="h-4 w-4" />
                                    </button>
                                </TooltipTrigger>
                                <TooltipContent>{interactionMode === "pan" ? "Exit Pan Mode" : "Pan Mode"}</TooltipContent>
                            </Tooltip>
                            <Tooltip>
                                <TooltipTrigger asChild>
                                    <button onClick={() => setMode(interactionMode === "snip" ? "text" : "snip")} className={`rounded p-1 ${interactionMode === "snip" ? "bg-(--info-w10) text-(--info-wMain)" : "hover:bg-(--secondary-w20)"}`}>
                                        <Scissors className="h-4 w-4" />
                                    </button>
                                </TooltipTrigger>
                                <TooltipContent>{interactionMode === "snip" ? "Exit Snip Mode" : "Snip Selection"}</TooltipContent>
                            </Tooltip>
                            {interactionMode === "snip" && snipStatus !== "idle" && (
                                <div
                                    className={`ml-2 rounded px-2 py-0.5 text-xs ${snipStatus === "processing" ? "bg-(--info-w10) text-(--info-wMain)" : snipStatus === "success" ? "bg-(--success-w10) text-(--success-wMain)" : "bg-(--error-w10) text-(--error-wMain)"}`}
                                >
                                    {snipStatus === "processing" ? "Processing..." : snipStatus === "success" ? "Done!" : "Failed"}
                                </div>
                            )}
                        </>
                    )}
                </div>
            </div>
            <div
                ref={viewerRef}
                className={`relative w-full flex-grow overflow-auto rounded border bg-(--background-w10) ${interactionMode === "text" ? "select-text" : ""}`}
                onMouseDown={handleMouseDown}
                onMouseMove={handleMouseMove}
                onMouseUp={handleMouseUp}
                onMouseLeave={handleMouseUp}
                onWheel={handleWheel}
                style={{ cursor: getCursor() }}
            >
                {isWaitingForHighlight && (
                    <div className="absolute inset-0 z-50 flex items-center justify-center bg-(--background-w10)">
                        <div className="text-sm text-(--secondary-text-wMain)">Loading PDF...</div>
                    </div>
                )}

                {selection && getSelectionStyle() && <div style={getSelectionStyle()!} />}

                <Document
                    options={pdfOptions}
                    file={resolvedUrl}
                    onLoadSuccess={onDocumentLoadSuccess}
                    onLoadError={onDocumentLoadError}
                    loading={<div className="p-4 text-center">Loading PDF...</div>}
                    error={<div className="p-4 text-center text-(--error-wMain)">Failed to load PDF.</div>}
                >
                    <div ref={documentContainerRef} style={{ transform: `translate(${pan.x}px, ${pan.y}px)` }}>
                        {numPages &&
                            Array.from(new Array(numPages), (_, index) => (
                                <div
                                    key={`page_${index + 1}`}
                                    ref={el => {
                                        if (el) {
                                            pageRefs.current.set(index + 1, el);
                                        } else {
                                            pageRefs.current.delete(index + 1);
                                        }
                                    }}
                                    className="flex justify-center p-2"
                                >
                                    <Page
                                        pageNumber={index + 1}
                                        scale={zoomLevel}
                                        onLoadSuccess={(page: { width: number }) => {
                                            if (index === 0 && !pageWidth) {
                                                setPageWidth(page.width);
                                            }
                                        }}
                                        renderTextLayer={interactionMode === "text" || citationMaps.length > 0}
                                        renderAnnotationLayer={true}
                                        className="shadow-lg"
                                    />
                                </div>
                            ))}
                    </div>
                </Document>
            </div>
        </div>
    );
};

export default PdfRenderer;
