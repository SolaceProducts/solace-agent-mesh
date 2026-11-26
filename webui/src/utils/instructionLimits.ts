// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const DEFAULT_AGENT_INSTRUCTION_MAX = 32000;

export const agentInstructionLength = (value: string): number => [...value].length;
