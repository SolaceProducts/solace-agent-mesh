// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { z } from "zod";

const DOCKER_IMAGE_REPO_PATTERN = /^[a-zA-Z0-9][a-zA-Z0-9._/:-]*[a-zA-Z0-9]$|^[a-zA-Z0-9]$/;
const DOCKER_IMAGE_TAG_PATTERN = /^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/;
const K8S_SECRET_NAME_PATTERN = /^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$/;

interface ValidationRule {
    check: (val: string) => boolean;
    message: string;
}

const optionalValidatedString = (maxLength: number, maxLengthLabel: string, pattern: RegExp, patternMessage: string, extraRules: ValidationRule[] = []) =>
    z
        .string()
        .optional()
        .superRefine((val, ctx) => {
            if (!val?.trim()) return;
            const trimmed = val.trim();
            const rules: ValidationRule[] = [{ check: v => v.length > maxLength, message: `${maxLengthLabel} cannot exceed ${maxLength} characters` }, ...extraRules, { check: v => !pattern.test(v), message: patternMessage }];
            for (const rule of rules) {
                if (rule.check(trimmed)) {
                    ctx.addIssue({ code: "custom", message: rule.message });
                    return;
                }
            }
        });

export const dockerImageRepoSchema = optionalValidatedString(255, "Image repository", DOCKER_IMAGE_REPO_PATTERN, "Must contain only alphanumeric characters, dots, hyphens, underscores, and forward slashes", [
    { check: v => v.includes(" "), message: "Image repository cannot contain spaces" },
    { check: v => /:(?!.*\/)/.test(v), message: "Image repository should not include a tag — use the Image Tag field for the tag" },
]);

export const dockerImageTagSchema = optionalValidatedString(128, "Image tag", DOCKER_IMAGE_TAG_PATTERN, "Image tag must start with an alphanumeric character and contain only alphanumeric characters, dots, hyphens, and underscores");

export const registrySecretNameSchema = optionalValidatedString(
    253,
    "Registry secret name",
    K8S_SECRET_NAME_PATTERN,
    "Must be a valid Kubernetes secret name: lowercase alphanumeric characters, dots, and hyphens only, must start and end with an alphanumeric character"
);

export const createHeaderValidation = () => {
    return z
        .array(
            z.object({
                key: z.string(),
                value: z.string(),
            })
        )
        .refine(
            headers => {
                const keys = headers.filter(h => h.key && h.key.trim() !== "").map(h => h.key.trim().toLowerCase());
                return keys.length === new Set(keys).size;
            },
            {
                message: "Header names must be unique",
            }
        );
};
