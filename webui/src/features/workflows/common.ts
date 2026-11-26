// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { z } from "zod";

import { SAFE_NAME_DESCRIPTION, SAFE_NAME_PATTERN, SAFE_TEXT_DESCRIPTION, SAFE_TEXT_PATTERN } from "@/common/form";

export const workflowInfoSchema = z.object({
    name: z
        .string()
        .transform(val => val.trim())
        .pipe(
            z
                .string()
                .min(3, "Name must be at least 3 characters")
                .max(255, "Name must be 255 characters or less")
                .refine(val => SAFE_NAME_PATTERN.test(val), { message: SAFE_NAME_DESCRIPTION })
        ),
    description: z
        .string()
        .transform(val => val.trim())
        .pipe(
            z
                .string()
                .min(10, "Description must be at least 10 characters")
                .max(2000, "Description must be 2000 characters or less")
                .refine(val => SAFE_TEXT_PATTERN.test(val), { message: SAFE_TEXT_DESCRIPTION })
        ),
    version: z
        .string()
        .transform(val => val.trim())
        .pipe(
            z
                .string()
                .max(64, "Version must be 64 characters or less")
                .refine(val => val === "" || SAFE_NAME_PATTERN.test(val), { message: SAFE_NAME_DESCRIPTION })
        ),
});

export type WorkflowInfoFormData = z.infer<typeof workflowInfoSchema>;
