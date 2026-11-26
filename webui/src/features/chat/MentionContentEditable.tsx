// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import * as React from "react";
import DOMPurify from "dompurify";

import { getDisplayText, INTERNAL_MENTION_REGEX } from "@/utils/mentionUtils";

import type { Person } from "@/types/people";

import { cn } from "@/utils";

interface MentionContentEditableProps {
    value: string;
    onChange: (value: string) => void;
    onKeyDown?: (event: React.KeyboardEvent) => void;
    placeholder?: string;
    disabled?: boolean;
    className?: string;
    onPaste?: (event: React.ClipboardEvent) => void;
    cursorPosition?: number;
    mentionMap?: Map<string, Person>;
    disambiguatedIds?: Set<string>;
}

const MentionContentEditable = React.forwardRef<HTMLDivElement, MentionContentEditableProps>(({ value, onChange, onKeyDown, placeholder, disabled, className, onPaste, cursorPosition, mentionMap, disambiguatedIds }, ref) => {
    const editableRef = React.useRef<HTMLDivElement>(null);
    const isUpdatingRef = React.useRef(false);

    React.useImperativeHandle(ref, () => editableRef.current!);

    const escapeHtml = React.useCallback((str: string) => {
        return str.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#039;").replace(/\n/g, "<br>");
    }, []);

    const renderContent = React.useCallback(
        (text: string) => {
            if (!text) return "";

            const parts: string[] = [];
            let lastIndex = 0;

            const regex = new RegExp(INTERNAL_MENTION_REGEX.source, "g");
            let match;

            while ((match = regex.exec(text)) !== null) {
                const matchStart = match.index;
                const matchEnd = matchStart + match[0].length;
                const name = match[1];
                const id = match[2];

                if (matchStart > lastIndex) {
                    parts.push(escapeHtml(text.substring(lastIndex, matchStart)));
                }

                const person = mentionMap?.get(id);

                const needsDisambiguation = disambiguatedIds?.has(id) ?? false;

                let displayText: string;
                if (person) {
                    displayText = getDisplayText(person, needsDisambiguation);
                } else {
                    displayText = `@${name}`;
                }

                parts.push(
                    `<span class="mention-chip" contenteditable="false" ` +
                        `data-internal="${escapeHtml(match[0])}" ` +
                        `data-person-id="${escapeHtml(id)}" ` +
                        `data-person-name="${escapeHtml(name)}" ` +
                        `data-display="${escapeHtml(displayText)}"` +
                        `>${escapeHtml(displayText)}</span>`
                );

                lastIndex = matchEnd;
            }

            if (lastIndex < text.length) {
                parts.push(escapeHtml(text.substring(lastIndex)));
            }

            return parts.join("");
        },
        [mentionMap, disambiguatedIds, escapeHtml]
    );

    const extractPlainText = React.useCallback((element: HTMLElement): string => {
        const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT | NodeFilter.SHOW_ELEMENT, {
            acceptNode: (node: Node) => {
                if (node.nodeType === Node.TEXT_NODE) {
                    if (node.parentElement?.classList.contains("mention-chip")) {
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
                parts.push(node.textContent ?? "");
            } else if (node.nodeType === Node.ELEMENT_NODE) {
                const el = node as HTMLElement;
                if (el.classList.contains("mention-chip")) {
                    const internal = el.getAttribute("data-internal");
                    if (internal) {
                        parts.push(internal);
                    } else {
                        const personId = el.getAttribute("data-person-id");
                        const personName = el.getAttribute("data-person-name");
                        if (personId && personName) {
                            parts.push(`@[${personName}](${personId})`);
                        } else {
                            parts.push(el.textContent ?? "");
                        }
                    }
                } else if (el.tagName === "BR") {
                    parts.push("\n");
                }
            }
        }

        return parts.join("");
    }, []);

    const handleInput = React.useCallback(() => {
        if (isUpdatingRef.current || !editableRef.current) return;

        const editable = editableRef.current;

        if (!editable.textContent && !editable.querySelector(".mention-chip")) {
            if (editable.innerHTML !== "") {
                editable.innerHTML = "";
            }
            onChange("");
            return;
        }

        const plainText = extractPlainText(editable);
        onChange(plainText);
    }, [onChange, extractPlainText]);

    const setCursorPosition = React.useCallback((offset: number) => {
        if (!editableRef.current) return;

        const selection = window.getSelection();
        if (!selection) return;

        let currentOffset = 0;
        let targetNode: Node | null = null;
        let targetOffset = 0;

        const walker = document.createTreeWalker(editableRef.current, NodeFilter.SHOW_TEXT | NodeFilter.SHOW_ELEMENT, {
            acceptNode: (node: Node) => {
                if (node.nodeType === Node.TEXT_NODE) {
                    if (node.parentElement?.classList.contains("mention-chip")) {
                        return NodeFilter.FILTER_REJECT;
                    }
                }
                return NodeFilter.FILTER_ACCEPT;
            },
        });

        let node: Node | null;
        while ((node = walker.nextNode())) {
            if (node.nodeType === Node.TEXT_NODE) {
                const nodeLength = node.textContent?.length ?? 0;
                if (currentOffset + nodeLength >= offset) {
                    targetNode = node;
                    targetOffset = offset - currentOffset;
                    break;
                }
                currentOffset += nodeLength;
            } else if (node.nodeType === Node.ELEMENT_NODE) {
                const el = node as HTMLElement;
                if (el.classList.contains("mention-chip")) {
                    const internal = el.getAttribute("data-internal") ?? "";
                    const mentionLength = internal.length;

                    if (currentOffset + mentionLength > offset) {
                        if (!el.nextSibling) {
                            targetNode = el.parentNode;
                            targetOffset = Array.from(el.parentNode?.childNodes || []).indexOf(el) + 1;
                        } else {
                            targetNode = el.nextSibling;
                            targetOffset = 0;
                        }
                        break;
                    }
                    currentOffset += mentionLength;
                } else if (el.tagName === "BR") {
                    if (currentOffset + 1 > offset) {
                        targetNode = el.parentNode;
                        targetOffset = Array.from(el.parentNode?.childNodes || []).indexOf(el);
                        break;
                    }
                    currentOffset += 1;
                }
            }
        }

        try {
            if (targetNode) {
                const range = document.createRange();
                range.setStart(targetNode, targetOffset);
                range.collapse(true);
                selection.removeAllRanges();
                selection.addRange(range);
            } else {
                const range = document.createRange();
                range.selectNodeContents(editableRef.current);
                range.collapse(false);
                selection.removeAllRanges();
                selection.addRange(range);
            }
        } catch {
            editableRef.current?.focus();
        }
    }, []);

    React.useEffect(() => {
        if (!editableRef.current || isUpdatingRef.current) {
            return;
        }

        const currentPlainText = extractPlainText(editableRef.current);

        if (currentPlainText !== value) {
            isUpdatingRef.current = true;

            editableRef.current.innerHTML = renderContent(value);

            setTimeout(() => {
                if (cursorPosition !== undefined) {
                    setCursorPosition(cursorPosition);
                }
                isUpdatingRef.current = false;
            }, 0);
        }
    }, [value, renderContent, extractPlainText, setCursorPosition, cursorPosition]);

    const handleCopy = React.useCallback(
        (e: React.ClipboardEvent<HTMLDivElement>) => {
            if (!editableRef.current) return;

            const selection = window.getSelection();
            if (!selection || selection.rangeCount === 0) return;

            const range = selection.getRangeAt(0);
            const fragment = range.cloneContents();
            const div = document.createElement("div");
            div.appendChild(fragment);

            const internalText = extractPlainText(div);

            const displayText = internalText.replace(INTERNAL_MENTION_REGEX, "@$1");

            e.clipboardData?.setData("text/plain", displayText);
            e.clipboardData?.setData("text/html", div.innerHTML);
            e.preventDefault();
        },
        [extractPlainText]
    );

    const handlePaste = React.useCallback(
        (e: React.ClipboardEvent<HTMLDivElement>) => {
            if (onPaste) {
                onPaste(e);
            }

            if (!e.defaultPrevented) {
                e.preventDefault();

                const html = e.clipboardData.getData("text/html");
                const text = e.clipboardData.getData("text/plain");

                if (html?.includes("mention-chip")) {
                    const sanitizedHtml = DOMPurify.sanitize(html);
                    const tempDiv = document.createElement("div");
                    tempDiv.innerHTML = sanitizedHtml;

                    const processedContent = Array.from(tempDiv.childNodes)
                        .map(node => {
                            if (node.nodeType === Node.ELEMENT_NODE) {
                                const el = node as HTMLElement;
                                if (el.classList.contains("mention-chip")) {
                                    return el.outerHTML;
                                }
                            }
                            return node.textContent ?? "";
                        })
                        .join("");

                    const sanitizedContent = DOMPurify.sanitize(processedContent, {
                        ALLOWED_TAGS: ["span", "br"],
                        ALLOWED_ATTR: ["class", "contenteditable", "data-internal", "data-person-id", "data-person-name", "data-display"],
                    });

                    document.execCommand("insertHTML", false, sanitizedContent);
                } else {
                    document.execCommand("insertText", false, text);
                }
            }
        },
        [onPaste]
    );

    return (
        <div className="relative">
            <div
                ref={editableRef}
                contentEditable={!disabled}
                onInput={handleInput}
                onKeyDown={onKeyDown}
                onCopy={handleCopy}
                onPaste={handlePaste}
                className={cn("w-full outline-none", !value && placeholder ? "empty" : "", disabled ? "cursor-not-allowed opacity-50" : "", className)}
                data-testid="chat-input"
                data-placeholder={placeholder}
                suppressContentEditableWarning
            />

            {!value && placeholder && <div className="pointer-events-none absolute inset-0 truncate p-3 text-base/normal text-(--secondary-text-w50)">{placeholder}</div>}
        </div>
    );
});

MentionContentEditable.displayName = "MentionContentEditable";

export { MentionContentEditable };
export type { MentionContentEditableProps };
