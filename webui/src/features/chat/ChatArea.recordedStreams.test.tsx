// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { type DrawnBubble, readDrawnBubbles, readRecordedStream, recordedStreamNames, replayStream } from "@/testing/helpers/recordedStreams";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { ChatArea } from "./ChatArea";

const MESSAGE_END = "data-message-end";
const MESSAGE_FAILED = "This message couldn't be displayed.";

interface MarkdownTable {
    rows: number;
}

function collapse(text: string): string {
    return text.replace(/\s+/g, " ").trim();
}

function isTableLine(line: string): boolean {
    return line.trimStart().startsWith("|");
}

function isDelimiterLine(line: string): boolean {
    return /^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$/.test(line);
}

function splitMarkdown(text: string): { tables: MarkdownTable[]; prose: string[] } {
    const lines = text.split("\n");
    const tables: MarkdownTable[] = [];
    const prose: string[] = [];
    let inFence = false;
    for (let i = 0; i < lines.length; i++) {
        const line = lines[i];
        if (/^\s*(```|~~~)/.test(line)) {
            inFence = !inFence;
            continue;
        }
        const startsBlock = i === 0 || lines[i - 1].trim() === "";
        if (!inFence && startsBlock && isTableLine(line) && i + 1 < lines.length && isDelimiterLine(lines[i + 1])) {
            let rows = 0;
            i += 2;
            while (i < lines.length && isTableLine(lines[i])) {
                rows++;
                i++;
            }
            tables.push({ rows });
            i--;
            continue;
        }
        prose.push(line);
    }
    return { tables, prose };
}

function visibleProse(line: string): string {
    return collapse(
        line
            .replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")
            .replace(/^\s*(#{1,6}\s+|>\s*|[-*+]\s+|\d+\.\s+)/, "")
            .replaceAll("**", "")
            .replaceAll("__", "")
            .replaceAll("`", "")
    );
}

function bubbleNodes(container: HTMLElement, index: number): HTMLElement[] {
    const end = container.querySelector<HTMLElement>(`[${MESSAGE_END}="${index}"]`);
    expect(end, `message ${index} was not drawn`).not.toBeNull();
    const nodes: HTMLElement[] = [];
    for (let node = end!.previousElementSibling; node && !node.hasAttribute(MESSAGE_END); node = node.previousElementSibling) {
        nodes.unshift(node as HTMLElement);
    }
    return nodes;
}

function expectBubbleDrawn(nodes: HTMLElement[], want: DrawnBubble) {
    const text = collapse(nodes.map(n => n.textContent ?? "").join(" "));
    const cards = nodes.flatMap(n => within(n).queryAllByRole("button", { name: /^Preview / })).map(card => card.getAttribute("aria-label")!.replace(/^Preview /, ""));
    const tables = nodes.flatMap(n => [...n.querySelectorAll("table")]).map(table => table.querySelectorAll("tbody tr").length);
    const drawnText = want.segments.filter(s => s.kind === "text").map(s => s.text ?? "");

    expect(text).not.toContain(MESSAGE_FAILED);
    expect(cards).toEqual(want.segments.filter(s => s.kind === "file" || s.kind === "artifact").map(s => s.name));

    const expected = drawnText.map(splitMarkdown);
    expect(tables).toEqual(expected.flatMap(e => e.tables.map(t => t.rows)));
    for (const line of expected
        .flatMap(e => e.prose)
        .map(visibleProse)
        .filter(Boolean)) {
        expect(text).toContain(line);
    }
    for (const markup of ["«", "»", "{{", "{%"]) {
        if (!drawnText.some(t => t.includes(markup))) {
            expect(text).not.toContain(markup);
        }
    }

    let from = 0;
    for (const segment of want.segments) {
        const marker =
            segment.kind === "text"
                ? splitMarkdown(segment.text ?? "")
                      .prose.map(visibleProse)
                      .find(Boolean)
                : segment.name;
        if (!marker) continue;
        const at = text.indexOf(marker, from);
        expect(at, `${segment.kind} ${JSON.stringify(marker)} is drawn out of order`).toBeGreaterThanOrEqual(0);
        from = at + marker.length;
    }

    const notice = nodes.flatMap(n => [...n.querySelectorAll("[data-testid^='inlineNotice-']")]).map(n => n.getAttribute("data-testid"));
    if (want.isWarning) {
        expect(notice).toEqual(["inlineNotice-warning"]);
    } else if (want.isError) {
        expect(notice).toEqual(["inlineNotice-error"]);
    } else {
        expect(notice).toEqual([]);
    }
}

describe("ChatArea draws recorded task streams the way sam task render reports them", () => {
    it.each(recordedStreamNames())("%s", name => {
        const messages = replayStream(readRecordedStream(name));
        const want = readDrawnBubbles(name);
        const { container } = renderWithProviders(<ChatArea renderMessageAddon={(_, index) => <hr {...{ [MESSAGE_END]: index }} />} />, {
            chatContextValues: { messages, isResponding: false, isLoadingSession: false, agents: [] },
        });

        expect(container.textContent, "a message hit the error boundary").not.toContain(MESSAGE_FAILED);
        const drawn = messages.flatMap((message, index) => (message.isUser || message.isStatusBubble ? [] : [index]));
        expect(drawn).toHaveLength(want.length);
        drawn.forEach((index, bubble) => expectBubbleDrawn(bubbleNodes(container, index), want[bubble]));
    });
});
