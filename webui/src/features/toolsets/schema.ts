// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { z } from "zod";

import { SAFE_NAME_DESCRIPTION, SAFE_NAME_PATTERN, SAFE_TEXT_DESCRIPTION, SAFE_TEXT_PATTERN } from "@/common/form";

export const toolsetBaseSchema = z.object({
    name: z
        .string()
        .transform(val => val.trim())
        .pipe(
            z
                .string()
                .min(3, "Name must be at least 3 characters")
                .max(64, "Name must be 64 characters or less")
                .regex(/^[a-zA-Z0-9_-]+$/, "Name may only contain letters, digits, underscores, and hyphens")
                .refine(val => SAFE_NAME_PATTERN.test(val), { message: SAFE_NAME_DESCRIPTION })
        ),
    description: z
        .string()
        .transform(val => val.trim())
        .pipe(
            z
                .string()
                .min(10, "Description must be at least 10 characters")
                .max(1000, "Description must be 1000 characters or less")
                .refine(val => SAFE_TEXT_PATTERN.test(val), { message: SAFE_TEXT_DESCRIPTION })
        ),
});

export type ToolsetBaseFormData = z.infer<typeof toolsetBaseSchema>;
