// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { A2UISurface } from "@/types";

export interface AnswerEntry {
    question: string;
    answer: string;
    contentType?: string;
}

export function lit(val: unknown): string {
    if (typeof val === "string") return val;
    if (val && typeof val === "object" && "literalString" in val) return String((val as { literalString: string }).literalString);
    return "";
}

function resolveAnswerValue(raw: unknown, other: string): string {
    if (Array.isArray(raw)) {
        const selected = (raw as string[]).filter(v => v !== "__other__");
        if ((raw as string[]).includes("__other__") && other) selected.push(other);
        return selected.join(", ");
    }
    if (raw === "__other__" && other) return other;
    if (typeof raw === "string") return raw;
    if (typeof raw === "number" || typeof raw === "boolean") return String(raw);
    if (raw == null) return "";
    return JSON.stringify(raw);
}

export function buildAnswerEntries(surface: A2UISurface, context: Record<string, unknown>): AnswerEntry[] {
    const answers = context.answers as Record<string, unknown> | undefined;
    const otherText = context.other_text as Record<string, string> | undefined;
    if (!answers) return [];

    const compById = new Map<string, Record<string, unknown>>();
    for (const c of surface.components) {
        if (c.id) compById.set(c.id, c);
    }

    const entries: AnswerEntry[] = [];
    for (const qKey of Object.keys(answers).sort((a, b) => a.localeCompare(b))) {
        const displayValue = resolveAnswerValue(answers[qKey], otherText?.[qKey] ?? "");
        if (!displayValue) continue;

        const labelComp = compById.get(`${qKey}-label`);
        entries.push({ question: labelComp ? lit(labelComp.text) : qKey, answer: displayValue });
    }

    return entries;
}

export function buildApprovalEntries(surface: A2UISurface, decision: string): AnswerEntry[] {
    const compById = new Map<string, Record<string, unknown>>();
    for (const c of surface.components) {
        if (c.id) compById.set(c.id, c);
    }

    const infoText = lit(compById.get("info")?.text);
    const entries: AnswerEntry[] = [{ question: infoText || "Request", answer: decision }];

    for (const c of surface.components) {
        const id = c.id ?? "";
        if (id.startsWith("arg-") && id.endsWith("-label")) {
            const key = id.slice("arg-".length, -"-label".length);
            const valueComp = compById.get(`arg-${key}-value`);
            const value = lit(valueComp?.text);
            if (value) {
                const contentType = typeof valueComp?.contentType === "string" ? valueComp.contentType : undefined;
                entries.push({ question: lit(c.text) || key, answer: value, contentType });
            }
        }
    }

    return entries;
}

export function buildAnsweredResponseText(surface: A2UISurface, source: "ask_user_question" | "tool_approval", data: Record<string, unknown>): string {
    if (source === "tool_approval") {
        const decision = data.approved === false ? "Denied" : "Approved";
        return JSON.stringify(buildApprovalEntries(surface, decision));
    }
    const entries = buildAnswerEntries(surface, data);
    return entries.length > 0 ? JSON.stringify(entries) : "";
}
