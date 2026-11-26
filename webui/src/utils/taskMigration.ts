// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ParsedTaskData } from "@/types/storage";

export const CURRENT_SCHEMA_VERSION = 1;

type MigrationFunction = (task: ParsedTaskData) => ParsedTaskData;

const migrateV0ToV1: MigrationFunction = task => {
    return {
        ...task,
        taskMetadata: {
            ...task.taskMetadata,
            schemaVersion: 1,
        },
    };
};

const MIGRATIONS: Record<number, MigrationFunction> = {
    0: migrateV0ToV1,
};

export const migrateTask = (task: ParsedTaskData): ParsedTaskData => {
    const version = task.taskMetadata?.schemaVersion ?? 0;

    if (version >= CURRENT_SCHEMA_VERSION) {
        return task;
    }

    let migratedTask = task;
    for (let v = version; v < CURRENT_SCHEMA_VERSION; v++) {
        const migrationFunc = MIGRATIONS[v];

        if (migrationFunc) {
            migratedTask = migrationFunc(migratedTask);
            console.log(`[Migration] Migrated task ${task.taskId} from v${v} to v${v + 1}`);
        } else {
            console.warn(`[Migration] No migration function found for version ${v}`);
        }
    }

    return migratedTask;
};

export const migrateTasks = (tasks: ParsedTaskData[]): ParsedTaskData[] => {
    return tasks.map(migrateTask);
};

export const getCurrentSchemaVersion = (): number => {
    return CURRENT_SCHEMA_VERSION;
};
