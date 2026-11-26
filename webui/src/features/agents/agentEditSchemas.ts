// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { z } from "zod";

import { SAFE_TEXT_DESCRIPTION, SAFE_TEXT_PATTERN } from "@/common/form";

import { agentInstructionLength, DEFAULT_AGENT_INSTRUCTION_MAX } from "@/utils/instructionLimits";

export const agentInstructionsSchema = z.object({
    systemPrompt: z
        .string()
        .transform(val => val.trim())
        .pipe(
            z
                .string()
                .min(1, "Instructions are required")
                .refine(val => agentInstructionLength(val) <= DEFAULT_AGENT_INSTRUCTION_MAX, {
                    error: ({ input }) => `Instructions must be ${DEFAULT_AGENT_INSTRUCTION_MAX.toLocaleString()} characters or fewer (currently ${agentInstructionLength(String(input)).toLocaleString()})`,
                })
                .refine(val => SAFE_TEXT_PATTERN.test(val), {
                    message: SAFE_TEXT_DESCRIPTION,
                })
        ),
});

export const agentToolsetsSchema = z.object({
    selectedToolsets: z.array(z.string()).min(0),
});

export const agentModelSchema = z.object({
    selectedModel: z.string().min(1, "A model must be selected"),
});

export const agentConnectorsSchema = z.object({
    selectedConnectors: z.array(z.string()).min(0, "No connectors selected"),
});
