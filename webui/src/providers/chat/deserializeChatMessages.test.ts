// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { isHiddenWorkingPart, partArtifactName } from "@/features/chat/artifactTags";

import type { MessageFE } from "@/types";

import { deserializeChatMessages } from "./deserializeChatMessages";
import { serializeChatMessage } from "./serializeChatMessage";

function makeTask(bubbles: Array<{ type: "user" | "agent"; [key: string]: unknown }>, taskId = "task-1") {
    return {
        taskId,
        messageBubbles: bubbles,
        createdAt: 1000,
    };
}

describe("deserializeChatMessages", () => {
    describe("HIL / userInputRequest restore", () => {
        it("restores an answered input request (followed by continuation) as responded, so the intake form is not re-shown on re-open", () => {
            const result = deserializeChatMessages(
                makeTask([
                    { id: "m-form", type: "agent", parts: [], userInputRequest: { requestId: "r1", responded: false } },
                    { id: "m-after", type: "agent", parts: [{ kind: "text", text: "continuing..." }] },
                ]),
                "session-1"
            );
            expect(result[0].userInputRequest?.responded).toBe(true);
        });

        it("leaves a trailing (last-bubble) input request untouched, so a genuinely-pending HIL form still renders live", () => {
            const result = deserializeChatMessages(
                makeTask([
                    { id: "m-prior", type: "user", parts: [{ kind: "text", text: "hi" }] },
                    { id: "m-form", type: "agent", parts: [], userInputRequest: { requestId: "r1", responded: false } },
                ]),
                "session-1"
            );
            const form = result.find(m => m.userInputRequest);
            expect(form?.userInputRequest?.responded).toBe(false);
        });

        it("preserves responded:true on a trailing request (does not clobber the short-circuit)", () => {
            const result = deserializeChatMessages(makeTask([{ id: "m-form", type: "agent", parts: [], userInputRequest: { requestId: "r1", responded: true } }]), "session-1");
            expect(result[0].userInputRequest?.responded).toBe(true);
        });

        it("leaves a trailing request with no responded field untouched (legacy shape stays live)", () => {
            const result = deserializeChatMessages(makeTask([{ id: "m-form", type: "agent", parts: [], userInputRequest: { requestId: "r1" } }]), "session-1");
            expect(result[0].userInputRequest?.responded).toBeFalsy();
        });

        it("with two stacked requests, marks the earlier one responded and leaves the trailing one live", () => {
            const result = deserializeChatMessages(
                makeTask([
                    { id: "m-form1", type: "agent", parts: [], userInputRequest: { requestId: "r1", responded: false } },
                    { id: "m-form2", type: "agent", parts: [], userInputRequest: { requestId: "r2", responded: false } },
                ]),
                "session-1"
            );
            expect(result[0].userInputRequest?.responded).toBe(true);
            expect(result[1].userInputRequest?.responded).toBe(false);
        });
    });

    describe("basic deserialization", () => {
        it("deserializes a user bubble", () => {
            const result = deserializeChatMessages(makeTask([{ id: "msg-1", type: "user", text: "hello", parts: [{ kind: "text", text: "hello" }] }]), "session-1");

            expect(result).toHaveLength(1);
            expect(result[0].isUser).toBe(true);
            expect(result[0].role).toBe("user");
            expect(result[0].taskId).toBe("task-1");
            expect(result[0].isComplete).toBe(true);
            expect(result[0].metadata?.messageId).toBe("msg-1");
            expect(result[0].metadata?.sessionId).toBe("session-1");
        });

        it("deserializes an agent bubble", () => {
            const result = deserializeChatMessages(makeTask([{ id: "msg-2", type: "agent", parts: [{ kind: "text", text: "response" }] }]), "session-1");

            expect(result).toHaveLength(1);
            expect(result[0].isUser).toBe(false);
            expect(result[0].role).toBe("agent");
        });

        it("deserializes multiple bubbles in order", () => {
            const result = deserializeChatMessages(
                makeTask([
                    { type: "user", parts: [{ kind: "text", text: "q" }] },
                    { type: "agent", parts: [{ kind: "text", text: "a" }] },
                ]),
                "session-1"
            );

            expect(result).toHaveLength(2);
            expect(result[0].isUser).toBe(true);
            expect(result[1].isUser).toBe(false);
        });

        it("falls back to bubble.text when parts are missing", () => {
            const result = deserializeChatMessages(makeTask([{ type: "agent", text: "fallback text" }]), "session-1");

            expect(result[0].parts).toContainEqual(expect.objectContaining({ kind: "text", text: "fallback text" }));
        });

        it("preserves createdAt from task", () => {
            const result = deserializeChatMessages(makeTask([{ type: "user", parts: [{ kind: "text", text: "hi" }] }]), "session-1");

            expect(result[0].createdAt).toBe(1000);
        });

        it("normalizes an ISO 8601 createdAt (the chatTasks API wire shape) to epoch ms", () => {
            const task = { taskId: "task-1", messageBubbles: [{ type: "user" as const, parts: [{ kind: "text" as const, text: "hi" }] }], createdAt: "2026-06-08T15:25:37.940+00:00" };
            const result = deserializeChatMessages(task, "session-1");

            expect(result[0].createdAt).toBe(Date.parse("2026-06-08T15:25:37.940+00:00"));
        });

        it("falls back to 0 for an unparseable createdAt (never NaN, which would break divider placement)", () => {
            const task = { taskId: "task-1", messageBubbles: [{ type: "user" as const, parts: [{ kind: "text" as const, text: "hi" }] }], createdAt: "not-a-date" };
            const result = deserializeChatMessages(task, "session-1");

            expect(result[0].createdAt).toBe(0);
        });
    });

    describe("artifact marker extraction", () => {
        it("extracts artifact_return markers from text", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "agent",
                        parts: [{ kind: "text", text: "Here is the file «artifact_return:report.pdf»" }],
                    },
                ]),
                "session-1"
            );

            const artifactPart = result[0].parts.find(p => p.kind === "artifact");
            expect(artifactPart).toBeDefined();
            expect(artifactPart).toMatchObject({
                kind: "artifact",
                status: "completed",
                name: "report.pdf",
            });
        });

        it("extracts artifact: markers from text", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "agent",
                        parts: [{ kind: "text", text: "Created «artifact:data.csv»" }],
                    },
                ]),
                "session-1"
            );

            const artifactPart = result[0].parts.find(p => p.kind === "artifact");
            expect(artifactPart).toMatchObject({ name: "data.csv" });
        });

        it("strips version suffix from artifact filenames", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "agent",
                        parts: [{ kind: "text", text: "«artifact_return:file.md:0»" }],
                    },
                ]),
                "session-1"
            );

            const artifactPart = result[0].parts.find(p => p.kind === "artifact");
            expect(artifactPart).toMatchObject({ name: "file.md" });
        });

        it("removes artifact markers from text content", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "agent",
                        parts: [{ kind: "text", text: "Here is «artifact_return:file.txt» your file" }],
                    },
                ]),
                "session-1"
            );

            const textPart = result[0].parts.find(p => p.kind === "text");
            expect(textPart).toMatchObject({ text: "Here is  your file" });
        });

        it("removes status update markers from text", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "agent",
                        parts: [{ kind: "text", text: "«status_update:processing»\nActual response" }],
                    },
                ]),
                "session-1"
            );

            const textPart = result[0].parts.find(p => p.kind === "text");
            expect(textPart).toMatchObject({ text: "Actual response" });
        });

        it("removes status update markers emitted with block-style triple guillemets", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "agent",
                        parts: [{ kind: "text", text: "«««status_update:Updating the plan...»»»\nActual response" }],
                    },
                ]),
                "session-1"
            );

            const textPart = result[0].parts.find(p => p.kind === "text");
            expect(textPart).toMatchObject({ text: "Actual response" });
        });

        it("removes artifact_return markers emitted with block-style triple guillemets", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "agent",
                        parts: [{ kind: "text", text: "Here is «««artifact_return:file.txt»»» your file" }],
                    },
                ]),
                "session-1"
            );

            const textPart = result[0].parts.find(p => p.kind === "text");
            expect(textPart).toMatchObject({ text: "Here is  your file" });
        });

        it("deduplicates artifacts from bubble.text and parts", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "agent",
                        text: "«artifact_return:file.txt»",
                        parts: [{ kind: "text", text: "«artifact_return:file.txt»" }],
                    },
                ]),
                "session-1"
            );

            const artifactParts = result[0].parts.filter(p => p.kind === "artifact");
            expect(artifactParts).toHaveLength(1);
        });

        it("extracts multiple different artifacts", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "agent",
                        parts: [{ kind: "text", text: "«artifact_return:a.txt»«artifact_return:b.txt»" }],
                    },
                ]),
                "session-1"
            );

            const artifactParts = result[0].parts.filter(p => p.kind === "artifact");
            expect(artifactParts).toHaveLength(2);
        });

        it("builds correct artifact URI with session ID", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "agent",
                        parts: [{ kind: "text", text: "«artifact_return:doc.pdf»" }],
                    },
                ]),
                "ses-42"
            );

            const artifactPart = result[0].parts.find(p => p.kind === "artifact");
            expect(artifactPart).toMatchObject({
                file: { uri: "artifact://ses-42/doc.pdf" },
            });
        });
    });

    describe("existing artifact parts", () => {
        it("keeps artifact parts that are not duplicates", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "agent",
                        parts: [{ kind: "artifact", name: "file.txt", status: "completed" }],
                    },
                ]),
                "session-1"
            );

            expect(result[0].parts).toHaveLength(1);
            expect(result[0].parts[0]).toMatchObject({ kind: "artifact", name: "file.txt" });
        });

        it("skips duplicate artifact parts already added from markers", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "agent",
                        text: "«artifact_return:file.txt»",
                        parts: [
                            { kind: "text", text: "response" },
                            { kind: "artifact", name: "file.txt", status: "completed" },
                        ],
                    },
                ]),
                "session-1"
            );

            const artifactParts = result[0].parts.filter(p => p.kind === "artifact");
            expect(artifactParts).toHaveLength(1);
        });

        it("keeps a __working tag through a real serialize → JSON → deserialize round-trip", () => {
            const live: MessageFE = {
                role: "agent",
                isUser: false,
                taskId: "task-1",
                isComplete: true,
                parts: [
                    { kind: "text", text: "Deck ready." },
                    { kind: "artifact", name: "deck-spec.yaml", status: "completed", tags: ["__working"], file: { name: "deck-spec.yaml", uri: "artifact://session-1/deck-spec.yaml" } },
                ],
                metadata: { messageId: "m1" },
            };
            const stored = JSON.parse(JSON.stringify(serializeChatMessage(live)));

            const result = deserializeChatMessages(makeTask([stored]), "session-1");

            const artifactParts = result[0].parts.filter(p => p.kind === "artifact");
            expect(artifactParts).toHaveLength(1);
            expect(artifactParts[0]).toMatchObject({ name: "deck-spec.yaml", tags: ["__working"] });
        });

        it("keeps a returned file part's __working tag through a real serialize → JSON → deserialize round-trip", () => {
            const live: MessageFE = {
                role: "agent",
                isUser: false,
                taskId: "task-1",
                isComplete: true,
                parts: [
                    { kind: "text", text: "Deck ready." },
                    { kind: "file", file: { name: "deck-spec.yaml", uri: "artifact://session-1/deck-spec.yaml?version=1" }, metadata: { tags: ["__working"] } },
                ],
                metadata: { messageId: "m1" },
            };
            const stored = JSON.parse(JSON.stringify(serializeChatMessage(live)));

            const result = deserializeChatMessages(makeTask([stored]), "session-1");

            const fileParts = result[0].parts.filter(p => p.kind === "file");
            expect(fileParts).toEqual([expect.objectContaining({ metadata: { tags: ["__working"] } })]);
        });

        it("keeps a returned file part's __working tag when its marker reaches bubble.text", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "agent",
                        text: "Deck ready. «artifact_return:deck-spec.yaml»",
                        parts: [
                            { kind: "text", text: "Deck ready." },
                            { kind: "file", file: { name: "deck-spec.yaml", uri: "artifact://session-1/deck-spec.yaml?version=1" }, metadata: { tags: ["__working"] } },
                        ],
                    },
                ]),
                "session-1"
            );

            const deckParts = result[0].parts.filter(p => partArtifactName(p) === "deck-spec.yaml");
            expect(deckParts).toHaveLength(1);
            expect(isHiddenWorkingPart(deckParts[0])).toBe(true);
        });

        it("keeps tags on an artifact restored from an «artifact:NAME» marker", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "agent",
                        text: "«artifact:deck-spec.yaml»",
                        parts: [{ kind: "artifact", name: "deck-spec.yaml", status: "completed", tags: ["__working"] }],
                    },
                ]),
                "session-1"
            );

            const artifactParts = result[0].parts.filter(p => p.kind === "artifact");
            expect(artifactParts).toHaveLength(1);
            expect(artifactParts[0]).toMatchObject({ name: "deck-spec.yaml", tags: ["__working"] });
        });

        it("matches tags across a versioned marker filename", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "agent",
                        text: "«artifact_return:deck-spec.yaml:2»",
                        parts: [{ kind: "artifact", name: "deck-spec.yaml", status: "completed", tags: ["__working"] }],
                    },
                ]),
                "session-1"
            );

            const artifactParts = result[0].parts.filter(p => p.kind === "artifact");
            expect(artifactParts).toHaveLength(1);
            expect(artifactParts[0]).toMatchObject({ name: "deck-spec.yaml", tags: ["__working"] });
        });

        it("leaves untagged artifacts without a tags field", () => {
            const result = deserializeChatMessages(makeTask([{ type: "agent", text: "«artifact_return:report.pdf»" }]), "session-1");

            const artifactParts = result[0].parts.filter(p => p.kind === "artifact");
            expect(artifactParts[0]).not.toHaveProperty("tags");
        });
    });

    describe("builder-private artifact suppression", () => {
        it("RETAINS the build_manifest.yaml artifact part so the Build Plan card restores on reload", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "agent",
                        parts: [{ kind: "text", text: "Plan ready. «artifact_return:build_manifest.yaml»" }],
                    },
                ]),
                "session-1"
            );

            const artifactParts = result[0].parts.filter(p => p.kind === "artifact");
            expect(artifactParts).toHaveLength(1);
            expect(artifactParts[0]).toMatchObject({ kind: "artifact", name: "build_manifest.yaml" });

            const textPart = result[0].parts.find(p => p.kind === "text");
            expect(textPart).toMatchObject({ text: "Plan ready. " });
        });

        it("does not synthesize artifact parts for platform_components.json or apply_complete.json markers", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "agent",
                        parts: [{ kind: "text", text: "«artifact:platform_components.json»«artifact_return:apply_complete.json»" }],
                    },
                ]),
                "session-1"
            );

            expect(result[0].parts.filter(p => p.kind === "artifact")).toHaveLength(0);
        });

        it("retains versioned build_manifest.yaml markers (version suffix stripped)", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "agent",
                        parts: [{ kind: "text", text: "«artifact_return:build_manifest.yaml:3»" }],
                    },
                ]),
                "session-1"
            );

            const artifactParts = result[0].parts.filter(p => p.kind === "artifact");
            expect(artifactParts).toHaveLength(1);
            expect(artifactParts[0]).toMatchObject({ name: "build_manifest.yaml" });
        });

        it("retains a persisted build_manifest.yaml artifact part on the pass-through branch", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "agent",
                        parts: [
                            { kind: "text", text: "done" },
                            { kind: "artifact", name: "build_manifest.yaml", status: "completed" },
                        ],
                    },
                ]),
                "session-1"
            );

            const artifactParts = result[0].parts.filter(p => p.kind === "artifact");
            expect(artifactParts).toHaveLength(1);
            expect(artifactParts[0]).toMatchObject({ name: "build_manifest.yaml" });
        });

        it("surfaces both the build manifest and other non-private artifacts together", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "agent",
                        parts: [{ kind: "text", text: "«artifact_return:build_manifest.yaml»«artifact_return:report.pdf»" }],
                    },
                ]),
                "session-1"
            );

            const names = result[0].parts.filter(p => p.kind === "artifact").map(p => (p as { name: string }).name);
            expect(names).toEqual(expect.arrayContaining(["build_manifest.yaml", "report.pdf"]));
            expect(names).toHaveLength(2);
        });

        it("drops gateway-resolved file parts for builder-private filenames on restore, since the pass-through branch would otherwise render them as generic file cards", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "agent",
                        parts: [
                            { kind: "text", text: "Here's your build plan." },
                            { kind: "file", file: { name: "build_manifest.yaml", mimeType: "application/vnd.sam-build-manifest+yaml", uri: "artifact://session-1/build_manifest.yaml?version=0" } },
                        ],
                    },
                ]),
                "session-1"
            );

            expect(result[0].parts.filter(p => p.kind === "file")).toHaveLength(0);
            expect(result[0].parts.find(p => p.kind === "text")).toMatchObject({ text: "Here's your build plan." });
        });

        it("drops file parts for platform_components.json and apply_complete.json too", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "agent",
                        parts: [
                            { kind: "file", file: { name: "platform_components.json" } },
                            { kind: "file", file: { name: "apply_complete.json" } },
                        ],
                    },
                ]),
                "session-1"
            );

            expect(result[0].parts.filter(p => p.kind === "file")).toHaveLength(0);
        });

        it("keeps non-private file parts through the pass-through branch", () => {
            const filePart = { kind: "file" as const, file: { name: "report.csv", mimeType: "text/csv" } };
            const result = deserializeChatMessages(makeTask([{ type: "agent", parts: [filePart] }]), "session-1");

            expect(result[0].parts.filter(p => p.kind === "file")).toHaveLength(1);
            expect(result[0].parts[0]).toMatchObject({ kind: "file", file: { name: "report.csv" } });
        });
    });

    describe("non-text parts", () => {
        it("passes through file parts unchanged", () => {
            const filePart = { kind: "file" as const, file: { name: "img.png", bytes: "base64data" } };
            const result = deserializeChatMessages(makeTask([{ type: "agent", parts: [filePart] }]), "session-1");

            expect(result[0].parts).toContainEqual(filePart);
        });

        it("passes through data parts unchanged", () => {
            const dataPart = { kind: "data" as const, data: { type: "deep_research_progress" } };
            const result = deserializeChatMessages(makeTask([{ type: "agent", parts: [dataPart] }]), "session-1");

            expect(result[0].parts).toContainEqual(dataPart);
        });

        it("drops a persisted request_build_activate signal on restore (so reload can't re-fire a deploy)", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "agent",
                        parts: [
                            { kind: "data", data: { type: "request_build_activate" } },
                            { kind: "text", text: "Deploying your build now." },
                        ],
                    },
                ]),
                "session-1"
            );
            expect(result[0].parts.some(p => p.kind === "data" && (p as { data?: { type?: string } }).data?.type === "request_build_activate")).toBe(false);
            expect(result[0].parts.find(p => p.kind === "text")).toMatchObject({ text: "Deploying your build now." });
        });
    });

    describe("metadata preservation", () => {
        it("preserves displayHtml for user messages", () => {
            const result = deserializeChatMessages(makeTask([{ type: "user", displayHtml: "<b>@User</b> hello", parts: [{ kind: "text", text: "hello" }] }]), "session-1");

            expect(result[0].displayHtml).toBe("<b>@User</b> hello");
        });

        it("preserves contextQuote and contextQuoteSourceId", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "user",
                        contextQuote: "quoted text",
                        contextQuoteSourceId: "task-0",
                        parts: [{ kind: "text", text: "followup" }],
                    },
                ]),
                "session-1"
            );

            expect(result[0].contextQuote).toBe("quoted text");
            expect(result[0].contextQuoteSourceId).toBe("task-0");
        });

        it("preserves collaborative session sender info", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "user",
                        sender_display_name: "Alice",
                        sender_email: "alice@example.com",
                        parts: [{ kind: "text", text: "hi" }],
                    },
                ]),
                "session-1"
            );

            expect(result[0].senderDisplayName).toBe("Alice");
            expect(result[0].senderEmail).toBe("alice@example.com");
        });

        it("preserves isError flag", () => {
            const result = deserializeChatMessages(makeTask([{ type: "agent", isError: true, parts: [{ kind: "text", text: "error" }] }]), "session-1");

            expect(result[0].isError).toBe(true);
        });

        it("includes progressUpdates when present", () => {
            const updates = [{ type: "status" as const, text: "Working", timestamp: 123 }];
            const result = deserializeChatMessages(makeTask([{ type: "agent", progressUpdates: updates, parts: [{ kind: "text", text: "done" }] }]), "session-1");

            expect(result[0].progressUpdates).toEqual(updates);
        });

        it("omits progressUpdates when empty", () => {
            const result = deserializeChatMessages(makeTask([{ type: "agent", progressUpdates: [], parts: [{ kind: "text", text: "done" }] }]), "session-1");

            expect(result[0]).not.toHaveProperty("progressUpdates");
        });

        it("includes thinkingContent when present", () => {
            const result = deserializeChatMessages(makeTask([{ type: "agent", thinkingContent: "reasoning...", isThinkingComplete: true, parts: [{ kind: "text", text: "answer" }] }]), "session-1");

            expect(result[0].thinkingContent).toBe("reasoning...");
            expect(result[0].isThinkingComplete).toBe(true);
        });

        it("defaults isThinkingComplete to true", () => {
            const result = deserializeChatMessages(makeTask([{ type: "agent", thinkingContent: "thinking", parts: [{ kind: "text", text: "answer" }] }]), "session-1");

            expect(result[0].isThinkingComplete).toBe(true);
        });

        it("omits thinkingContent when empty", () => {
            const result = deserializeChatMessages(makeTask([{ type: "agent", thinkingContent: "", parts: [{ kind: "text", text: "answer" }] }]), "session-1");

            expect(result[0]).not.toHaveProperty("thinkingContent");
        });
    });

    describe("empty / edge cases", () => {
        it("returns empty array for task with no bubbles", () => {
            const result = deserializeChatMessages(makeTask([]), "session-1");
            expect(result).toEqual([]);
        });

        it("drops text parts that are only whitespace after marker removal", () => {
            const result = deserializeChatMessages(
                makeTask([
                    {
                        type: "agent",
                        parts: [{ kind: "text", text: "«artifact_return:file.txt»  " }],
                    },
                ]),
                "session-1"
            );

            const textParts = result[0].parts.filter(p => p.kind === "text");
            expect(textParts).toHaveLength(0);
        });
    });

    describe("render-intent metadata", () => {
        it("restores persisted hidden flag so builder auto-prompts stay hidden on reload", () => {
            const result = deserializeChatMessages(makeTask([{ id: "m1", type: "user", text: "Run this test now", metadata: { hidden: true } }]), "session-1");
            expect(result[0].metadata?.hidden).toBe(true);
            expect(result[0].metadata?.messageId).toBe("m1");
            expect(result[0].metadata?.sessionId).toBe("session-1");
        });

        it("restores chip and applyChangesCard metadata", () => {
            const chip = { emoji: "✅", label: "Plan approved" };
            const applyChangesCard = { componentName: "X", componentKind: "agent", items: [] };
            const result = deserializeChatMessages(makeTask([{ id: "m1", type: "user", text: "x", metadata: { chip, applyChangesCard } }]), "session-1");
            expect(result[0].metadata?.chip).toEqual(chip);
            expect(result[0].metadata?.applyChangesCard).toEqual(applyChangesCard);
        });

        it("leaves metadata flags undefined for legacy bubbles without a metadata field", () => {
            const result = deserializeChatMessages(makeTask([{ id: "m1", type: "user", text: "x" }]), "session-1");
            expect(result[0].metadata?.hidden).toBeUndefined();
            expect(result[0].metadata?.messageId).toBe("m1");
        });

        it("does not let persisted metadata clobber the base messageId/sessionId fields", () => {
            const result = deserializeChatMessages(makeTask([{ id: "m1", type: "user", text: "x", metadata: { hidden: true, sessionId: "other", messageId: "other" } as never }]), "session-1");
            expect(result[0].metadata?.sessionId).toBe("session-1");
            expect(result[0].metadata?.messageId).toBe("m1");
            expect(result[0].metadata?.hidden).toBe(true);
        });
    });

    describe("authenticationLink restore", () => {
        it("restores a persisted resolved authenticationLink so the terminal state renders on re-open", () => {
            const authenticationLink = { url: "https://auth.example.com", text: "Authenticate", targetAgent: "orchestrator", outcome: "authenticated" };
            const result = deserializeChatMessages(makeTask([{ id: "m-auth", type: "agent", parts: [], authenticationLink }]), "session-1");
            expect(result[0].authenticationLink).toEqual(authenticationLink);
        });

        it("leaves authenticationLink undefined for bubbles without one", () => {
            const result = deserializeChatMessages(makeTask([{ id: "m1", type: "agent", parts: [{ kind: "text", text: "hi" }] }]), "session-1");
            expect(result[0].authenticationLink).toBeUndefined();
        });
    });
});
