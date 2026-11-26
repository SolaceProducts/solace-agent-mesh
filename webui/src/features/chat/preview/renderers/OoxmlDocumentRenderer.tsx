// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useRef, useState } from "react";
import { Loader2 } from "lucide-react";

import { useIsBinaryArtifactPreviewEnabled } from "@/hooks/useIsBinaryArtifactPreviewEnabled";

import { isChunkLoadError } from "@/utils/staleChunkRecovery";

import { base64ToArrayBuffer } from "@/utils";

import { MAX_ZIP_ENTRY_BYTES, withTimeout } from "./withTimeout";

const ENABLE_HYPERLINKS_UNTIL_SANITISED = false;

interface OoxmlDocumentRendererProps {
    readonly content: string;
    readonly filename: string;
    readonly documentType: "docx" | "pptx" | "xlsx";
    readonly setRenderError: (error: string | null) => void;
}

interface OoxmlViewer {
    load(source: ArrayBuffer): Promise<void>;
    destroy(): void;
}

const LOAD_TIMEOUT_MS = 30_000;

function describeError(err: unknown): string {
    if (isChunkLoadError(err)) return "Couldn't load the document preview engine. Check your connection and reload the page.";
    return err instanceof Error ? err.message : String(err);
}

export function OoxmlDocumentRenderer({ content, filename, documentType, setRenderError }: OoxmlDocumentRendererProps) {
    const containerRef = useRef<HTMLDivElement>(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);
    const previewEnabled = useIsBinaryArtifactPreviewEnabled();

    useEffect(() => {
        if (!previewEnabled) return;
        const container = containerRef.current;
        if (!container) return;

        let disposed = false;
        let settled = false;
        let viewer: OoxmlViewer | null = null;

        const mount = document.createElement("div");
        mount.className = "h-full w-full";
        container.appendChild(mount);

        const safeDestroy = () => {
            try {
                viewer?.destroy();
            } catch (err) {
                console.warn("OOXML viewer destroy failed", { documentType, filename, err });
            }
            viewer = null;
        };

        const fail = (err: unknown) => {
            if (disposed || settled) return;
            settled = true;
            console.warn("OOXML preview failed", { documentType, filename, err });
            const message = describeError(err);
            setError(message);
            setRenderError(message);
            setLoading(false);
            safeDestroy();
        };

        setLoading(true);
        setError(null);
        setRenderError(null);

        (async () => {
            try {
                if (!content) throw new Error("No document content to preview.");
                const bytes = base64ToArrayBuffer(content);

                let v: OoxmlViewer;
                if (documentType === "docx") {
                    const { DocxScrollViewer } = await import("@silurus/ooxml/docx");
                    if (disposed) return;
                    v = new DocxScrollViewer(mount, { enableTextSelection: true, enableHyperlinks: ENABLE_HYPERLINKS_UNTIL_SANITISED, maxZipEntryBytes: MAX_ZIP_ENTRY_BYTES, onError: fail });
                } else if (documentType === "pptx") {
                    const { PptxScrollViewer } = await import("@silurus/ooxml/pptx");
                    if (disposed) return;
                    v = new PptxScrollViewer(mount, { enableTextSelection: true, enableHyperlinks: ENABLE_HYPERLINKS_UNTIL_SANITISED, maxZipEntryBytes: MAX_ZIP_ENTRY_BYTES, onError: fail });
                } else {
                    const { XlsxViewer } = await import("@silurus/ooxml/xlsx");
                    if (disposed) return;
                    v = new XlsxViewer(mount, { enableHyperlinks: ENABLE_HYPERLINKS_UNTIL_SANITISED, maxZipEntryBytes: MAX_ZIP_ENTRY_BYTES, onError: fail });
                }

                viewer = v;

                await withTimeout(v.load(bytes), LOAD_TIMEOUT_MS, "Preview timed out while rendering.");

                if (disposed || settled) return;
                settled = true;
                setRenderError(null);
                setLoading(false);
            } catch (err) {
                fail(err);
            }
        })();

        return () => {
            disposed = true;
            safeDestroy();
            mount.remove();
        };
    }, [content, documentType, filename, previewEnabled, setRenderError]);

    if (!previewEnabled) {
        return <div className="flex h-full w-full items-center justify-center p-6 text-center text-sm text-(--secondary-text-wMain)">Document preview is disabled by your administrator.</div>;
    }

    return (
        <div className="relative h-full w-full overflow-auto bg-white">
            {loading && (
                <div className="absolute inset-0 flex items-center justify-center gap-2 text-sm text-(--secondary-text-wMain)">
                    <Loader2 className="h-4 w-4 animate-spin" />
                    Rendering {documentType.toUpperCase()}…
                </div>
            )}
            {error && !loading && <div className="absolute inset-0 flex items-center justify-center bg-white p-6 text-center text-sm text-(--secondary-text-wMain)">{error}</div>}
            <div ref={containerRef} className="h-full w-full" />
        </div>
    );
}
