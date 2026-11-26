// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";
import { Check, ChevronDown, ChevronRight, CircleCheckBig, Clock, Copy, XCircle } from "lucide-react";

import { api } from "@/api/client";

import { useChatContext } from "@/providers/contexts";

import { copyToClipboard } from "@/utils/clipboard";

import { Button } from "@/ui/button";
import { Card } from "@/ui/card";

import type { MessageFE } from "@/types";

import { A2UISurfaceRenderer, useIsExpired } from "./A2UIRenderer";
import { ArgValue, isRichArgType, resolveArgRenderType } from "./ArgValue";
import { type AnswerEntry, buildAnsweredResponseText } from "./summary";
import type { SurfaceAction } from "./types";

function StatusBanner({ icon, text }: { icon: React.ReactNode; text: string }) {
    return (
        <div className="flex w-full items-center gap-2 rounded-lg border px-4 py-2.5 text-sm shadow-sm">
            {icon}
            {text}
        </div>
    );
}

const COLLAPSE_VALUE_THRESHOLD = 120;

function AnswerValue({ value, contentType }: Readonly<{ value: string; contentType?: string }>) {
    const [expanded, setExpanded] = useState(false);
    const [copied, setCopied] = useState(false);
    const copiedTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

    useEffect(
        () => () => {
            if (copiedTimer.current) clearTimeout(copiedTimer.current);
        },
        []
    );

    const renderType = resolveArgRenderType(value, contentType);
    const rich = isRichArgType(renderType);
    if (!rich && value.length <= COLLAPSE_VALUE_THRESHOLD && !value.includes("\n")) {
        return <div className="font-medium break-all">{value}</div>;
    }

    const onCopy = async () => {
        if (await copyToClipboard(value)) {
            setCopied(true);
            if (copiedTimer.current) clearTimeout(copiedTimer.current);
            copiedTimer.current = setTimeout(() => setCopied(false), 2000);
        }
    };

    return (
        <div className="mt-1 rounded-md border bg-(--background-w20)">
            <div className="flex items-center justify-between gap-2 px-2 py-1">
                <button type="button" onClick={() => setExpanded(e => !e)} className="flex items-center gap-1 text-xs text-(--secondary-text-wMain) hover:text-(--primary-text-wMain)">
                    {expanded ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
                    {expanded ? "Hide value" : "Show value"} ({value.length.toLocaleString()} chars)
                </button>
                <Button variant="ghost" size="sm" onClick={onCopy} tooltip="Copy to clipboard" className="h-6 gap-1 px-1.5 text-xs">
                    {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
                    {copied ? "Copied" : "Copy"}
                </Button>
            </div>
            {expanded &&
                (rich ? (
                    <div className="border-t p-2">
                        <ArgValue value={value} contentType={contentType} showCopy={false} />
                    </div>
                ) : (
                    <pre className="max-h-72 overflow-auto border-t px-3 py-2 font-mono text-xs break-words whitespace-pre-wrap">{value}</pre>
                ))}
        </div>
    );
}

function ResponseSummary({ icon, entries }: { icon: React.ReactNode; entries: AnswerEntry[] }) {
    return (
        <Card noPadding className="w-full !flex-row gap-2 overflow-hidden px-4 py-3 text-sm shadow-none">
            <div className="mt-0.5 shrink-0">{icon}</div>
            <div className="flex min-w-0 flex-col gap-3">
                {entries.map((entry, i) => (
                    <div key={i} className="min-w-0">
                        <span className="text-sm text-(--secondary-text-wMain)">{entry.question}</span>
                        <AnswerValue value={entry.answer} contentType={entry.contentType} />
                    </div>
                ))}
            </div>
        </Card>
    );
}

type InputRequestSource = NonNullable<MessageFE["userInputRequest"]>["source"];

export function AnsweredBanner({ source, responseText }: Readonly<{ source: InputRequestSource; responseText?: string }>) {
    let entries: AnswerEntry[] | null = null;
    if (responseText?.startsWith("[")) {
        try {
            entries = JSON.parse(responseText) as AnswerEntry[];
        } catch {}
    }
    const denied = source === "tool_approval" && entries?.[0]?.answer === "Denied";
    const icon = denied ? <XCircle className="size-4 text-(--error-wMain)" /> : <CircleCheckBig className="size-4 text-(--success-wMain)" />;
    if (entries && entries.length > 0) {
        return <ResponseSummary icon={icon} entries={entries} />;
    }
    return <StatusBanner icon={icon} text={responseText || "Response submitted"} />;
}

export function UserInputMessage({ message }: { message: MessageFE }) {
    const req = message.userInputRequest;
    const { setMessages, displayError } = useChatContext();
    const [isSubmitting, setIsSubmitting] = useState(false);
    const expired = useIsExpired(req?.expiresAt);

    const ctaRef = useRef<HTMLDivElement>(null);
    useEffect(() => {
        ctaRef.current?.scrollIntoView({ block: "end" });
    }, []);

    const updateMessage = useCallback(
        (update: Partial<NonNullable<MessageFE["userInputRequest"]>>) => {
            setMessages((prev: MessageFE[]) => prev.map(msg => (msg.metadata?.messageId === message.metadata?.messageId && msg.userInputRequest ? { ...msg, userInputRequest: { ...msg.userInputRequest, ...update } } : msg)));
        },
        [setMessages, message.metadata?.messageId]
    );

    const handleAction = useCallback(
        async (action: SurfaceAction) => {
            if (!req) return;
            setIsSubmitting(true);
            try {
                if (action.eventName === "cancel") {
                    await api.webui.post("/api/v1/userInput/respond", {
                        taskId: req.taskId,
                        agentName: req.agentName,
                        requestId: req.requestId,
                        status: "cancelled",
                        data: {},
                    });
                } else {
                    await api.webui.post("/api/v1/userInput/respond", {
                        taskId: req.taskId,
                        agentName: req.agentName,
                        requestId: req.requestId,
                        status: "answered",
                        data: action.context,
                    });
                }
                let responseText: string;
                if (action.eventName === "cancel") {
                    responseText = action.completionText;
                } else {
                    responseText = buildAnsweredResponseText(req.surface, req.source, action.context) || action.completionText;
                }
                updateMessage({ responded: true, responseText });
            } catch (err) {
                displayError({ title: "Failed to submit response", error: err instanceof Error ? err.message : "An unknown error occurred.", errorSource: "api" });
            } finally {
                setIsSubmitting(false);
            }
        },
        [req, updateMessage, displayError]
    );

    if (!req) return null;

    if (req.cancelled) {
        return <CancelledBanner />;
    }

    if (req.responded) {
        return <AnsweredBanner source={req.source} responseText={req.responseText} />;
    }
    if (req.timedOut || expired) {
        return <StatusBanner icon={<Clock className="size-4" />} text="Request expired" />;
    }

    return (
        <div ref={ctaRef}>
            <A2UISurfaceRenderer surface={req.surface} onAction={handleAction} disabled={isSubmitting} expiresAt={req.expiresAt} />
        </div>
    );
}

export function CancelledBanner() {
    return (
        <div className="flex w-full items-center gap-2 rounded-lg border px-4 py-2.5 text-sm shadow-sm">
            <XCircle className="size-4" />
            Request was cancelled
        </div>
    );
}

export function TimedOutBanner() {
    return (
        <div className="flex w-full items-center gap-2 rounded-lg border px-4 py-2.5 text-sm shadow-sm">
            <Clock className="size-4" />
            Request timed out
        </div>
    );
}
