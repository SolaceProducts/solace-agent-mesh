// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useMemo, useState } from "react";
import { ArrowLeftCircle, ArrowRightCircle, Clock, Database, Hash, Info, Maximize2, MessageSquare, Minimize2, Tag } from "lucide-react";

import type { DataPart, FilePart, Part as A2APart, TextPart } from "@/types/a2a";
import { type A2AEventSSEPayload } from "@/types/sse";

import { JSONViewer, MarkdownHTMLConverter } from "@/common";

interface EventCardProps {
    event: A2AEventSSEPayload;
}

const EventCard: React.FC<EventCardProps> = ({ event }) => {
    const [isExpanded, setIsExpanded] = useState(false);

    const toggleExpand = () => setIsExpanded(!isExpanded);

    const handleCardClick = (event: React.MouseEvent<HTMLDivElement>) => {
        if ((event.target as HTMLElement).closest('[data-no-toggle="true"]')) {
            return;
        }
        toggleExpand();
    };

    const formattedTimestamp = new Date(event.timestamp).toLocaleString(undefined, {
        year: "numeric",
        month: "short",
        day: "numeric",
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
    });

    const getDirectionColor = (direction: string) => {
        switch (direction?.toLowerCase()) {
            case "request":
                return "bg-(--accent-n0-w30)";
            case "status-update":
            case "artifact-update":
                return "bg-(--info-w10)";
            case "task":
                return "bg-(--success-w10)";
            default:
                return "";
        }
    };

    const getDirectionIcon = (direction: string) => {
        switch (direction?.toLowerCase()) {
            case "request":
                return <ArrowRightCircle size={16} className="mr-1.5 flex-shrink-0 text-(--accent-n0-w100)" />;
            case "task":
                return <ArrowLeftCircle size={16} className="mr-1.5 flex-shrink-0 text-(--success-wMain)" />;
            case "status-update":
                return <Info size={16} className="mr-1.5 flex-shrink-0 text-(--info-wMain)" />;
            case "artifact-update":
                return <Database size={16} className="mr-1.5 flex-shrink-0 text-(--info-wMain)" />;
            default:
                return <MessageSquare size={16} className="mr-1.5 flex-shrink-0 text-(--secondary-wMain)" />;
        }
    };

    let displaySourceEntity = event.source_entity;
    if ((event.direction === "status-update" || event.direction === "task") && event.full_payload?.result?.metadata?.agent_name) {
        displaySourceEntity = event.full_payload.result.metadata.agent_name;
    }

    const handleCardKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
        if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            toggleExpand();
        }
    };

    return (
        <div
            className="mb-4 overflow-hidden rounded-lg border bg-(--background-w10) shadow-lg"
            data-testid={`event-card-${event.direction || "unknown"}`}
            onClick={handleCardClick}
            role="button"
            tabIndex={0}
            onKeyDown={handleCardKeyDown}
            aria-expanded={isExpanded}
        >
            <div className="cursor-pointer border-b bg-(--secondary-w10) px-4 py-3">
                <div className="flex items-center justify-between">
                    <div className="flex items-center">
                        {getDirectionIcon(event.direction)}
                        <span className={`rounded-full px-2 py-0.5 text-xs font-semibold whitespace-nowrap ${getDirectionColor(event.direction || "unknown")}`}>{(event.direction || "unknown").toUpperCase()}</span>
                        <span className="ml-3 font-mono text-xs text-(--secondary-text-wMain)" title={event.solace_topic}>
                            Topic: {event.solace_topic?.length > 50 ? event.solace_topic.substring(0, 50) + "..." : event.solace_topic}
                        </span>
                    </div>
                    <div className="p-1.5 text-(--secondary-text-wMain)" title={isExpanded ? "Collapse Payload" : "Expand Payload"}>
                        {isExpanded ? <Minimize2 size={16} /> : <Maximize2 size={16} />}
                    </div>
                </div>
                <p className="mt-1.5 flex items-center text-xs text-(--secondary-text-wMain)">
                    <Clock size={12} className="mr-1 text-(--secondary-text-wMain)" /> {formattedTimestamp}
                </p>
            </div>

            <div className="px-4 py-3">
                <div className="grid grid-cols-1 gap-x-4 gap-y-2 text-sm md:grid-cols-2">
                    <div className="flex items-center" title={`Source: ${displaySourceEntity}`}>
                        <ArrowLeftCircle size={14} className="mr-1.5 flex-shrink-0 text-(--secondary-text-wMain)" />
                        <span className="mr-1 font-medium">From:</span>
                        <span className="truncate">{displaySourceEntity}</span>
                    </div>
                    <div className="flex items-center" title={`Target: ${event.target_entity}`}>
                        <ArrowRightCircle size={14} className="mr-1.5 flex-shrink-0 text-(--secondary-text-wMain)" />
                        <span className="mr-1 font-medium">To:</span>
                        <span className="truncate">{event.target_entity}</span>
                    </div>
                    {event.message_id && (
                        <div className="flex items-center" title={`Message ID: ${event.message_id}`}>
                            <Hash size={14} className="mr-1.5 flex-shrink-0 text-(--secondary-text-wMain)" />
                            <span className="mr-1 font-medium">Msg ID:</span>
                            <span className="truncate font-mono text-xs">{event.message_id}</span>
                        </div>
                    )}
                    {event.task_id && (
                        <div className="flex items-center" title={`Task ID: ${event.task_id}`}>
                            <Tag size={14} className="mr-1.5 flex-shrink-0 text-(--secondary-text-wMain)" />
                            <span className="mr-1 font-medium">Task ID:</span>
                            <span className="truncate font-mono text-xs">{event.task_id}</span>
                        </div>
                    )}
                </div>

                {event.payload_summary && (event.payload_summary.method || event.payload_summary.params_preview) && (
                    <div className="mt-3 border-t pt-3">
                        <h4 className="mb-1 text-xs font-semibold text-(--secondary-text-wMain)">Payload Summary:</h4>
                        <div className="rounded-md bg-(--secondary-w10) p-2 text-xs">
                            {event.payload_summary.method && (
                                <p className="font-mono">
                                    <strong>Method:</strong> {event.payload_summary.method}
                                </p>
                            )}
                            {event.payload_summary.params_preview && (
                                <p className="mt-0.5 font-mono">
                                    <strong>Preview:</strong> {event.payload_summary.params_preview}
                                </p>
                            )}
                        </div>
                    </div>
                )}

                {useMemo(() => {
                    let parts: A2APart[] | undefined = undefined;
                    const payload = event.full_payload;

                    if (payload?.method?.startsWith("tasks/")) {
                        parts = payload.params?.message?.parts;
                    } else if (payload?.result) {
                        if (payload.result.status?.message?.parts) {
                            parts = payload.result.status.message.parts;
                        }
                    }

                    if (parts && parts.length > 0) {
                        return (
                            <div className="mt-3 border-t pt-3">
                                <h4 className="mb-1.5 text-xs font-semibold text-(--secondary-text-wMain)">Message Content:</h4>
                                <div className="space-y-1.5">
                                    {parts.map((part, index) => (
                                        <div key={index} className="rounded-md bg-(--secondary-w10) p-2 text-sm">
                                            {part.kind === "text" && <MarkdownHTMLConverter>{(part as TextPart).text}</MarkdownHTMLConverter>}
                                            {part.kind === "file" && (
                                                <div>
                                                    <strong>File:</strong> {(part as FilePart).file.name || "N/A"}
                                                    <span className="ml-2 text-xs text-(--secondary-text-wMain)">(Type: {(part as FilePart).file.mimeType || "N/A"})</span>
                                                </div>
                                            )}
                                            {part.kind === "data" && (
                                                <div>
                                                    <strong>Data:</strong>
                                                    {(part as DataPart).metadata?.tool_name ? ` Tool Output for: ${(part as DataPart)?.metadata?.tool_name}` : ` ${JSON.stringify((part as DataPart).data).substring(0, 100)}...`}
                                                </div>
                                            )}
                                        </div>
                                    ))}
                                </div>
                            </div>
                        );
                    }
                    return null;
                }, [event.full_payload])}
            </div>

            {isExpanded && (
                <div className="border-t bg-(--secondary-w10)">
                    <div className="scrollbar-themed max-h-96 overflow-y-auto p-1" data-no-toggle="true">
                        <JSONViewer data={event.full_payload} />
                    </div>
                </div>
            )}
        </div>
    );
};

export default EventCard;
