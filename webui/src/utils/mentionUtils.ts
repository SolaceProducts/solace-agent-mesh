// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import DOMPurify from "dompurify";

import type { Mention, Person } from "@/types/people";

export const INTERNAL_MENTION_REGEX = /@\[([^\]]+)\]\(([^)]+)\)/g;

export function parseInternalMention(text: string): { name: string; id: string } | null {
    const match = /@\[([^\]]+)\]\(([^)]+)\)/.exec(text);
    if (!match) return null;
    return { name: match[1], id: match[2] };
}

export function formatInternalMention(person: Person): string {
    return `@[${person.displayName}](${person.id})`;
}

const MAX_MENTION_QUERY_LENGTH = 50;

export function detectMentionTrigger(text: string, cursorPosition: number): string | null {
    const textBeforeCursor = text.substring(0, cursorPosition).replaceAll("\u00A0", " ");

    const lastAtIndex = textBeforeCursor.lastIndexOf("@");
    if (lastAtIndex === -1) return null;

    const charBeforeAt = lastAtIndex > 0 ? textBeforeCursor[lastAtIndex - 1] : " ";
    if (charBeforeAt !== " " && charBeforeAt !== "\n") return null;

    const charAfterAt = lastAtIndex < text.length - 1 ? text[lastAtIndex + 1] : "";
    if (charAfterAt === "[") return null;

    const query = textBeforeCursor.substring(lastAtIndex + 1);

    if (query.includes("\n")) return null;

    if (query.startsWith(" ")) return null;

    if (query.includes("  ")) return null;

    if (query.length > MAX_MENTION_QUERY_LENGTH) return null;

    return query;
}

export function formatMentionDisplay(person: Person): string {
    return `@${person.displayName}`;
}

export function formatDisambiguatedMentionDisplay(person: Person): string {
    const disambiguator = person.workEmail || person.id;
    return `@${person.displayName} [${disambiguator}]`;
}

export function getDisplayText(person: Person, disambiguate: boolean): string {
    if (disambiguate) {
        return formatDisambiguatedMentionDisplay(person);
    }
    return formatMentionDisplay(person);
}

export function formatMentionForBackend(person: Person): string {
    return `${person.displayName} <user_id:${person.id}>`;
}

export function insertMention(text: string, cursorPosition: number, person: Person): { newText: string; newCursorPosition: number } {
    const textBeforeCursor = text.substring(0, cursorPosition);
    const textAfterCursor = text.substring(cursorPosition);

    const lastAtIndex = textBeforeCursor.lastIndexOf("@");

    const mentionInternal = formatInternalMention(person);
    const beforeMention = text.substring(0, lastAtIndex);
    const newText = beforeMention + mentionInternal + " " + textAfterCursor;
    const newCursorPosition = (beforeMention + mentionInternal + " ").length;

    return { newText, newCursorPosition };
}

export function extractInternalMentions(text: string): Array<{
    name: string;
    id: string;
    startIndex: number;
    endIndex: number;
    fullMatch: string;
}> {
    const mentions: Array<{
        name: string;
        id: string;
        startIndex: number;
        endIndex: number;
        fullMatch: string;
    }> = [];

    const regex = new RegExp(INTERNAL_MENTION_REGEX.source, "g");
    let match;
    while ((match = regex.exec(text)) !== null) {
        mentions.push({
            name: match[1],
            id: match[2],
            startIndex: match.index,
            endIndex: match.index + match[0].length,
            fullMatch: match[0],
        });
    }

    return mentions;
}

export function internalToDisplayText(text: string): string {
    return text.replace(INTERNAL_MENTION_REGEX, "@$1");
}

export function extractDisplayTextFromHTML(html: string): string {
    const sanitizedHtml = DOMPurify.sanitize(html);

    const tempDiv = document.createElement("div");
    tempDiv.innerHTML = sanitizedHtml;

    const walker = document.createTreeWalker(tempDiv, NodeFilter.SHOW_TEXT | NodeFilter.SHOW_ELEMENT, {
        acceptNode: (node: Node) => {
            if (node.nodeType === Node.TEXT_NODE) {
                const parent = node.parentElement;
                if (parent && parent.classList.contains("mention-chip")) {
                    return NodeFilter.FILTER_REJECT;
                }
            }
            return NodeFilter.FILTER_ACCEPT;
        },
    });

    const parts: string[] = [];
    let node: Node | null;

    while ((node = walker.nextNode())) {
        if (node.nodeType === Node.TEXT_NODE) {
            parts.push(node.textContent || "");
        } else if (node.nodeType === Node.ELEMENT_NODE) {
            const el = node as HTMLElement;
            if (el.classList.contains("mention-chip")) {
                parts.push(el.getAttribute("data-display") || el.textContent || "");
            } else if (el.tagName === "BR") {
                parts.push("\n");
            }
        }
    }

    return parts.join("");
}

export function extractMentionsFromDOM(element: HTMLElement): Map<string, Person> {
    const mentionMap = new Map<string, Person>();

    const mentionChips = element.querySelectorAll(".mention-chip");

    mentionChips.forEach(chip => {
        const personId = chip.getAttribute("data-person-id");
        const personName = chip.getAttribute("data-person-name");

        if (personId && personName) {
            mentionMap.set(personId, {
                id: personId,
                displayName: personName,
                workEmail: personId,
            });
        }
    });

    return mentionMap;
}

export function buildMessageFromDOM(element: HTMLElement): string {
    const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT | NodeFilter.SHOW_ELEMENT, {
        acceptNode: (node: Node) => {
            if (node.nodeType === Node.TEXT_NODE) {
                const parent = node.parentElement;
                if (parent && parent.classList.contains("mention-chip")) {
                    return NodeFilter.FILTER_REJECT;
                }
            }
            return NodeFilter.FILTER_ACCEPT;
        },
    });

    const parts: string[] = [];
    let node: Node | null;

    while ((node = walker.nextNode())) {
        if (node.nodeType === Node.TEXT_NODE) {
            parts.push(node.textContent || "");
        } else if (node.nodeType === Node.ELEMENT_NODE) {
            const el = node as HTMLElement;
            if (el.classList.contains("mention-chip")) {
                const personId = el.getAttribute("data-person-id");
                const personName = el.getAttribute("data-person-name");

                if (personId && personName) {
                    parts.push(`${personName} <user_id:${personId}>`);
                } else {
                    parts.push(el.getAttribute("data-display") || el.textContent || "");
                }
            } else if (el.tagName === "BR") {
                parts.push("\n");
            }
        }
    }

    return parts.join("");
}

export function extractMentions(text: string): Mention[] {
    const mentions: Mention[] = [];
    const internalMentions = extractInternalMentions(text);

    for (const m of internalMentions) {
        mentions.push({
            id: m.id,
            name: m.name,
            startIndex: m.startIndex,
            endIndex: m.endIndex,
        });
    }

    return mentions;
}
