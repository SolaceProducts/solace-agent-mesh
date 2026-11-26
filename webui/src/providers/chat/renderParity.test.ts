// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { isFileShownAsCard, isHiddenWorkingPart } from "@/features/chat/artifactTags";

import { agentBubbles, type DrawnBubble, type DrawnSegment, readDrawnBubbles, readRecordedStream, recordedStreamNames, replayStream } from "@/testing/helpers/recordedStreams";
import type { ArtifactPart, FilePart, MessageFE, TextPart } from "@/types";

function draw(message: MessageFE): DrawnBubble {
    const segments: DrawnSegment[] = [];
    let text = "";
    for (const part of message.parts ?? []) {
        if (part.kind === "text") {
            text += (part as TextPart).text;
            continue;
        }
        if ((part.kind !== "file" && part.kind !== "artifact") || isHiddenWorkingPart(part) || isFileShownAsCard(part, message.parts ?? [])) continue;
        if (text) segments.push({ kind: "text", text });
        text = "";
        segments.push(part.kind === "file" ? { kind: "file", name: (part as FilePart).file.name } : { kind: "artifact", name: (part as ArtifactPart).name, status: (part as ArtifactPart).status });
    }
    if (text) segments.push({ kind: "text", text });
    return {
        taskId: message.taskId ?? "",
        isError: !!message.isError,
        isWarning: !!message.isWarning,
        inputRequest: message.userInputRequest?.requestId ?? "",
        segments,
    };
}

const streams = recordedStreamNames();

describe("render parity with sam task render", () => {
    it("has recorded streams to compare", () => {
        expect(streams.length).toBeGreaterThan(0);
    });

    it.each(streams)("%s draws the same bubbles", name => {
        expect(agentBubbles(replayStream(readRecordedStream(name))).map(draw)).toEqual(readDrawnBubbles(name));
    });
});
