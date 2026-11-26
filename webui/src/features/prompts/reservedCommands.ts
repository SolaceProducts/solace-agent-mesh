// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const RESERVED_COMMANDS = ["create-template"] as const;

export type ReservedCommand = (typeof RESERVED_COMMANDS)[number];

export function isReservedCommand(command: string): boolean {
    return RESERVED_COMMANDS.includes(command as ReservedCommand);
}
