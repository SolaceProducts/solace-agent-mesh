// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import { cn } from "@/utils";

const URL_PATTERN = /https?:\/\/[^\s<>"']+/gi;

const SENTENCE_PUNCTUATION = ".,;:!?";

const CLOSER_TO_OPENER: Record<string, string> = { ")": "(", "]": "[", "}": "{" };

type Segment = { kind: "text"; value: string; at: number } | { kind: "link"; href: string; at: number };

function isUnopenedCloser(url: string): boolean {
    const opener = CLOSER_TO_OPENER[url.at(-1) ?? ""];
    return opener !== undefined && !url.slice(0, -1).includes(opener);
}

function splitTrailingPunctuation(match: string): [href: string, trailing: string] {
    let end = match.length;
    while (end > 0) {
        const candidate = match.slice(0, end);
        if (!SENTENCE_PUNCTUATION.includes(candidate.at(-1) ?? "") && !isUnopenedCloser(candidate)) {
            break;
        }
        end--;
    }
    return [match.slice(0, end), match.slice(end)];
}

function toSegments(text: string): Segment[] {
    const segments: Segment[] = [];
    let cursor = 0;
    for (const match of text.matchAll(URL_PATTERN)) {
        const [href, trailing] = splitTrailingPunctuation(match[0]);
        if (match.index > cursor) {
            segments.push({ kind: "text", value: text.slice(cursor, match.index), at: cursor });
        }
        if (href) {
            segments.push({ kind: "link", href, at: match.index });
        }
        if (trailing) {
            segments.push({ kind: "text", value: trailing, at: match.index + href.length });
        }
        cursor = match.index + match[0].length;
    }
    if (cursor < text.length) {
        segments.push({ kind: "text", value: text.slice(cursor), at: cursor });
    }
    return segments;
}

interface LinkifiedTextProps {
    text: string;
    className?: string;
}

export function LinkifiedText({ text, className }: Readonly<LinkifiedTextProps>) {
    const segments = useMemo(() => toSegments(text), [text]);

    return (
        <span className={cn("break-words whitespace-pre-line", className)}>
            {segments.map(segment =>
                segment.kind === "link" ? (
                    <a key={segment.at} href={segment.href} target="_blank" rel="noopener noreferrer" className="text-(--primary-wMain) underline decoration-(--primary-wMain) hover:text-(--primary-w100) hover:decoration-(--primary-w100)">
                        {segment.href}
                    </a>
                ) : (
                    segment.value
                )
            )}
        </span>
    );
}
