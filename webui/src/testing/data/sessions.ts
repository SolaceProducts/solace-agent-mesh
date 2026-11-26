// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Session } from "@/types/session";

import { populatedProject } from "./projects";

export const mockSessions: Session[] = [
    {
        id: "session-1",
        name: "Session 1",
        createdAt: new Date("2024-03-18T10:30:00Z").toISOString(),
        updatedAt: new Date("2024-03-20T14:22:00Z").toISOString(),
        project: { id: populatedProject.id, name: populatedProject.name },
    },
    {
        id: "session-2",
        name: "Session 2",
        createdAt: new Date("2024-03-15T09:15:00Z").toISOString(),
        updatedAt: new Date("2024-03-19T16:45:00Z").toISOString(),
        project: { id: populatedProject.id, name: populatedProject.name },
    },
    {
        id: "session-3",
        name: "Session 3",
        createdAt: new Date("2024-03-15T09:15:00Z").toISOString(),
        updatedAt: new Date("2024-03-19T16:45:00Z").toISOString(),
    },
];
