// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type MessageFE } from "@/types/chat";

export const mockMessages: MessageFE[] = [
    {
        isUser: false,
        parts: [
            {
                kind: "text",
                text: "Hi! I'm the Orchestrator Agent. How can I help?",
            },
        ],
        isComplete: true,
        metadata: { sessionId: "mock-session-id", lastProcessedEventSequence: 0 },
    },
    {
        isUser: true,
        parts: [
            {
                kind: "text",
                text: "Hello! I need help with a coding task.",
            },
        ],
        isComplete: true,
        metadata: { sessionId: "mock-session-id", lastProcessedEventSequence: 1 },
    },
    {
        isUser: false,
        parts: [
            {
                kind: "text",
                text: "I'd be happy to help with your coding task. Could you please provide more details about what you're working on?",
            },
        ],
        isComplete: true,
        metadata: { sessionId: "mock-session-id", lastProcessedEventSequence: 2 },
    },
];

export const mockLoadingMessage: MessageFE = {
    isUser: false,
    parts: [
        {
            kind: "text",
            text: "Working on your request...",
        },
    ],
    isStatusBubble: true,
    isComplete: false,
    taskId: "mock-task-id",
    metadata: { sessionId: "mock-session-id", lastProcessedEventSequence: 5 },
};
