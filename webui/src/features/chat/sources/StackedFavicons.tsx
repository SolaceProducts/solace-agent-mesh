// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { SearchSource } from "@/types/rag";

import { getFileTypeIcon } from "@/features/chat/file/FileIcon";

import { getCleanDomain, getFaviconUrl } from "@/utils";

interface StackedFaviconsProps {
    sources: SearchSource[];
    start?: number;
    end?: number;
    size?: number;
}

function SourceIcon({ source, className = "", size = 16 }: { source: SearchSource; className?: string; size?: number }) {
    const isDocument = source.sourceType === "document" || (source.filename && !source.link);

    if (isDocument && source.filename) {
        const iconSize = Math.round(size * 0.6);
        const icon = getFileTypeIcon(undefined, source.filename, { size: iconSize, className: "text-(--secondary-text-wMain)" });
        return (
            <div className={`relative box-content overflow-hidden rounded-full border border-(--secondary-w20) bg-(--secondary-w10) ${className}`} style={{ width: size, height: size }} title={source.filename}>
                <div className="flex h-full w-full items-center justify-center">{icon}</div>
            </div>
        );
    }

    const domain = getCleanDomain(source.link || "");
    return (
        <div className={`relative box-content overflow-hidden rounded-full border border-(--secondary-w20) bg-(--background-w10) ${className}`} style={{ width: size, height: size }}>
            <img
                src={getFaviconUrl(domain, size * 2)}
                alt={domain}
                className="relative h-full w-full"
                onError={e => {
                    const target = e.target as HTMLImageElement;
                    target.style.display = "none";
                    const parent = target.parentElement;
                    if (parent) {
                        parent.innerHTML = `<div class="bg-(--secondary-w10) text-(--secondary-text-wMain) flex h-full w-full items-center justify-center text-xs font-bold">${domain[0].toUpperCase()}</div>`;
                    }
                }}
            />
        </div>
    );
}

export function StackedFavicons({ sources, start = 0, end = 3, size = 16 }: StackedFaviconsProps) {
    const slice = start < 0 ? [start] : [start, end];
    const visibleSources = sources.slice(...slice);

    if (visibleSources.length === 0) {
        return null;
    }

    return (
        <div className="relative flex items-center" style={{ height: size }}>
            {visibleSources.map((source, i) => (
                <div
                    key={`favicon-${i}`}
                    className="relative"
                    style={{
                        marginLeft: i > 0 ? -4 : 0,
                        zIndex: i + 1,
                    }}
                >
                    <SourceIcon source={source} size={size} />
                </div>
            ))}
        </div>
    );
}

export default StackedFavicons;
