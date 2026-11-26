// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useMemo, useState } from "react";
import { Loader2 } from "lucide-react";

import { useExecutionArtifacts } from "@/api/scheduled-tasks";

import { MessageBanner } from "@/common/banners/MessageBanner";
import { MarkdownWrapper } from "@/common/markdown";

import { parseCitations } from "@/utils/citations";
import { downloadFile } from "@/utils/download";

import { ArtifactBar } from "@/features/chat/artifact/ArtifactBar";
import { ArtifactPreviewBody } from "@/features/scheduledTasks/ExecutionArtifactsView";

import type { ArtifactInfo } from "@/types";

const ARTIFACT_MARKER_REGEX = /«(artifact_return|artifact):([^»]+)»/g;

function stripVersionSuffix(name: string): string {
    if (!name.includes(":")) return name;
    const parts = name.split(":");
    const last = parts[parts.length - 1];
    return /^\d+$/.test(last) ? parts.slice(0, -1).join(":") : name;
}

interface Segment {
    kind: "text" | "artifact";
    value: string;
}

function splitAgentText(text: string): { segments: Segment[]; mentionedNames: Set<string> } {
    const segments: Segment[] = [];
    const mentioned = new Set<string>();
    if (!text) return { segments, mentionedNames: mentioned };

    let lastIndex = 0;
    let match: RegExpExecArray | null;
    const re = new RegExp(ARTIFACT_MARKER_REGEX.source, "g");
    while ((match = re.exec(text)) !== null) {
        if (match.index > lastIndex) {
            segments.push({ kind: "text", value: text.slice(lastIndex, match.index) });
        }
        const name = stripVersionSuffix(match[2]);
        if (!mentioned.has(name)) {
            mentioned.add(name);
            segments.push({ kind: "artifact", value: name });
        }
        lastIndex = re.lastIndex;
    }
    if (lastIndex < text.length) {
        segments.push({ kind: "text", value: text.slice(lastIndex) });
    }
    return { segments, mentionedNames: mentioned };
}

interface ExecutionOutputProps {
    sessionId: string | null | undefined;
    text: string;
    showPlaceholder?: boolean;
    hideArtifacts?: boolean;
    ragData?: unknown[];
}

interface RagSourceLike {
    citationId?: string;
}
interface RagEntryLike {
    sources?: RagSourceLike[];
}

function aggregateRagSources(ragData: unknown[] | undefined): RagEntryLike | undefined {
    if (!ragData || ragData.length === 0) return undefined;
    const entries = ragData as RagEntryLike[];
    if (entries.length === 1) return entries[0];

    const seen = new Set<string>();
    const uniqueSources: RagSourceLike[] = [];
    for (const entry of entries) {
        for (const source of entry?.sources ?? []) {
            const id = source?.citationId;
            if (!id || seen.has(id)) continue;
            seen.add(id);
            uniqueSources.push(source);
        }
    }
    return { ...entries[entries.length - 1], sources: uniqueSources };
}

export const ExecutionOutput: React.FC<ExecutionOutputProps> = ({ sessionId, text, showPlaceholder = true, hideArtifacts = false, ragData }) => {
    const { data: artifacts, isLoading, error } = useExecutionArtifacts(sessionId);

    const { segments, mentionedNames } = useMemo(() => splitAgentText(text), [text]);
    const ragMetadata = useMemo(() => aggregateRagSources(ragData), [ragData]);

    const artifactsByName = useMemo(() => {
        const map = new Map<string, ArtifactInfo>();
        for (const a of artifacts ?? []) {
            if (!map.has(a.filename)) map.set(a.filename, a);
        }
        return map;
    }, [artifacts]);

    const orphanArtifacts = useMemo(() => (hideArtifacts ? [] : (artifacts ?? []).filter(a => !mentionedNames.has(a.filename))), [hideArtifacts, artifacts, mentionedNames]);

    if (error && !hideArtifacts) {
        return <MessageBanner variant="error" message={error instanceof Error ? error.message : "Failed to load artifacts"} />;
    }

    if (!text && (!artifacts || artifacts.length === 0)) {
        return showPlaceholder ? <div className="text-(--secondary-text-wMain) italic">No output yet.</div> : null;
    }

    return (
        <div className="space-y-3 text-sm break-words">
            {segments.map((segment, idx) => {
                if (segment.kind === "text") {
                    const trimmed = segment.value.trim();
                    if (!trimmed) return null;
                    const citations = ragMetadata ? parseCitations(segment.value, ragMetadata as unknown as Parameters<typeof parseCitations>[1]) : undefined;
                    return <MarkdownWrapper key={`seg-${idx}`} content={segment.value} className="text-sm" citations={citations && citations.length > 0 ? citations : undefined} />;
                }
                if (hideArtifacts) return null;
                const artifact = artifactsByName.get(segment.value);
                return <InlineArtifact key={`seg-${idx}`} name={segment.value} artifact={artifact} sessionId={sessionId} isLoading={isLoading && !artifact} />;
            })}
            {orphanArtifacts.length > 0 && (
                <div className="space-y-2">
                    {orphanArtifacts.map(artifact => (
                        <InlineArtifact key={artifact.uri ?? `${artifact.filename}@${artifact.version ?? "latest"}`} name={artifact.filename} artifact={artifact} sessionId={sessionId} isLoading={false} />
                    ))}
                </div>
            )}
        </div>
    );
};

interface InlineArtifactProps {
    name: string;
    artifact: ArtifactInfo | undefined;
    sessionId: string | null | undefined;
    isLoading: boolean;
}

const InlineArtifact: React.FC<InlineArtifactProps> = ({ name, artifact, sessionId, isLoading }) => {
    const [expanded, setExpanded] = useState(false);

    if (!artifact) {
        return (
            <div className="overflow-hidden rounded-md border border-(--secondary-w40)">
                <ArtifactBar filename={name} status={isLoading ? "in-progress" : "completed"} context="chat" />
                {isLoading && (
                    <div className="flex items-center gap-2 border-t bg-(--background-w10) p-3 text-xs text-(--secondary-text-wMain)">
                        <Loader2 className="h-3 w-3 animate-spin" />
                        Loading…
                    </div>
                )}
            </div>
        );
    }

    const handleDownload = () => downloadFile({ name: artifact.filename, mimeType: artifact.mimeType, size: artifact.size, uri: artifact.uri }, sessionId ?? undefined);

    const expandedBody =
        expanded && sessionId ? (
            <div className="h-96 border-t bg-(--background-w10) p-4">
                <ArtifactPreviewBody artifact={artifact} sessionId={sessionId} onDownload={handleDownload} />
            </div>
        ) : null;

    return (
        <div className="overflow-hidden rounded-md border border-(--secondary-w40)">
            <ArtifactBar
                filename={artifact.filename}
                mimeType={artifact.mimeType}
                size={artifact.size}
                status="completed"
                context="chat"
                expandable
                expanded={expanded}
                onToggleExpand={() => setExpanded(v => !v)}
                expandedContent={expandedBody}
                actions={{ onDownload: handleDownload }}
            />
        </div>
    );
};
