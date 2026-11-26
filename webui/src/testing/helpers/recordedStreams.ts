// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { readdirSync, readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { processChatEvent } from "@/providers/chat/processChatEvent";

import type { MessageFE } from "@/types";

const streamsDir = resolve(dirname(fileURLToPath(import.meta.url)), "../../../..", "internal/samcli/render/testdata/streams");

export interface DrawnSegment {
    kind: string;
    text?: string;
    name?: string;
    status?: string;
}

export interface DrawnBubble {
    taskId: string;
    isError: boolean;
    isWarning: boolean;
    inputRequest: string;
    segments: DrawnSegment[];
}

export function recordedStreamNames(): string[] {
    return readdirSync(streamsDir)
        .filter(name => name.endsWith(".jsonl"))
        .map(name => name.replace(/\.jsonl$/, ""));
}

export function readRecordedStream(name: string): string[] {
    return readFileSync(join(streamsDir, `${name}.jsonl`), "utf8")
        .split("\n")
        .filter(Boolean);
}

export function readDrawnBubbles(name: string): DrawnBubble[] {
    return JSON.parse(readFileSync(join(streamsDir, `${name}.drawn.json`), "utf8")) as DrawnBubble[];
}

export function replayStream(lines: string[]): MessageFE[] {
    let messages: MessageFE[] = [];
    lines.forEach((line, index) => {
        const event = JSON.parse(line) as { data: unknown };
        const output = processChatEvent({
            eventData: JSON.stringify(event.data),
            messages,
            ragData: [],
            artifacts: [],
            flags: { showThinkingContent: false, ragEnabled: false, isReplaying: true },
            sessionId: "s1",
            selectedAgentName: "Orchestrator",
            deepResearchQueryHistory: new Map(),
            eventSequence: index + 1,
            isTaskRunningInBackground: () => false,
        });
        if (output.messages) messages = output.messages;
    });
    return messages;
}

export function agentBubbles(messages: MessageFE[]): MessageFE[] {
    return messages.filter(m => !m.isUser && !m.isStatusBubble);
}
