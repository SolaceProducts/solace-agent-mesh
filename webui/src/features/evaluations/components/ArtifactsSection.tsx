// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useChatContext } from "@/providers/contexts";

import { getErrorMessage } from "@/utils/api";
import { downloadBlob } from "@/utils/download";

import type { EvalArtifact } from "@/types/eval";

import { ArtifactBar } from "@/features/chat/artifact/ArtifactBar";
import { canPreviewArtifact } from "@/features/chat/preview";

import { coerceArtifactVersion, evalArtifactRenderType } from "../utils";

interface ArtifactsSectionProps {
    readonly artifacts: readonly EvalArtifact[];
    readonly fetchBlob: (artifact: EvalArtifact) => Promise<Blob>;
    readonly onPreview?: (artifact: EvalArtifact) => void;
}

export function ArtifactsSection({ artifacts, fetchBlob, onPreview }: ArtifactsSectionProps) {
    if (!artifacts.length) return null;

    return (
        <div className="flex w-full min-w-0 flex-col gap-2">
            {artifacts.map((a, idx) => (
                <ArtifactRow key={`${a.filename}-${a.version ?? ""}-${idx}`} artifact={a} fetchBlob={() => fetchBlob(a)} onPreview={onPreview ? () => onPreview(a) : undefined} />
            ))}
        </div>
    );
}

interface ArtifactRowProps {
    readonly artifact: EvalArtifact;
    readonly fetchBlob: () => Promise<Blob>;
    readonly onPreview?: () => void;
}

function ArtifactRow({ artifact, fetchBlob, onPreview }: ArtifactRowProps) {
    const status = artifact.snapshotFailed ? "failed" : "completed";
    const previewable =
        !artifact.snapshotFailed &&
        evalArtifactRenderType(artifact.filename, artifact.mimeType) !== null &&
        canPreviewArtifact({
            filename: artifact.filename,
            mimeType: artifact.mimeType ?? "",
            size: artifact.size ?? 0,
            lastModified: "",
        }).canPreview;

    const { displayError } = useChatContext();
    const handleDownload = async () => {
        try {
            const blob = await fetchBlob();
            downloadBlob(blob, artifact.filename);
        } catch (e) {
            displayError({ title: "Download failed", error: getErrorMessage(e), errorSource: "api" });
        }
    };

    return (
        <ArtifactBar
            filename={artifact.filename}
            mimeType={artifact.mimeType}
            size={artifact.size}
            version={coerceArtifactVersion(artifact.version)}
            status={status}
            error={status === "failed" ? (artifact.snapshotFailureReason ?? "Artifact snapshot failed") : undefined}
            context="list"
            actions={{
                onPreview: previewable && onPreview ? onPreview : undefined,
                onDownload: artifact.snapshotFailed
                    ? undefined
                    : () => {
                          void handleDownload();
                      },
            }}
        />
    );
}
