// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { dockerImageRepoSchema, dockerImageTagSchema, registrySecretNameSchema } from "./validationUtils";

const parse = (schema: typeof dockerImageRepoSchema, value: string | undefined) => schema.safeParse(value);

const expectSuccess = (schema: typeof dockerImageRepoSchema, value: string | undefined) => {
    expect(parse(schema, value).success).toBe(true);
};

const expectError = (schema: typeof dockerImageRepoSchema, value: string, messagePart: string) => {
    const result = parse(schema, value);
    expect(result.success).toBe(false);
    if (!result.success) {
        expect(result.error.issues[0].message).toContain(messagePart);
    }
};

describe("dockerImageRepoSchema", () => {
    test("accepts empty/undefined (optional field)", () => {
        expectSuccess(dockerImageRepoSchema, undefined);
        expectSuccess(dockerImageRepoSchema, "");
        expectSuccess(dockerImageRepoSchema, "   ");
    });

    test("accepts valid repository names", () => {
        expectSuccess(dockerImageRepoSchema, "nginx");
        expectSuccess(dockerImageRepoSchema, "myregistry/myimage");
        expectSuccess(dockerImageRepoSchema, "ghcr.io/org/repo");
        expectSuccess(dockerImageRepoSchema, "registry.example.com/team/app");
        expectSuccess(dockerImageRepoSchema, "a");
        expectSuccess(dockerImageRepoSchema, "my_image-v2.0");
    });

    test("allows registry with port", () => {
        expectSuccess(dockerImageRepoSchema, "localhost:5000/myimage");
        expectSuccess(dockerImageRepoSchema, "registry.example.com:5443/org/app");
    });

    test("rejects colons that look like a tag", () => {
        expectError(dockerImageRepoSchema, "nginx:latest", "tag");
        expectError(dockerImageRepoSchema, "myregistry/myimage:v1", "tag");
    });

    test("rejects spaces", () => {
        expectError(dockerImageRepoSchema, "my image", "spaces");
    });

    test("rejects names exceeding 255 characters", () => {
        expectError(dockerImageRepoSchema, "a".repeat(256), "255 characters");
    });

    test("rejects invalid characters", () => {
        expectError(dockerImageRepoSchema, "my@image", "alphanumeric");
        expectError(dockerImageRepoSchema, "image!", "alphanumeric");
        expectError(dockerImageRepoSchema, "-startwithdash", "alphanumeric");
    });
});

describe("dockerImageTagSchema", () => {
    test("accepts empty/undefined (optional field)", () => {
        expectSuccess(dockerImageTagSchema, undefined);
        expectSuccess(dockerImageTagSchema, "");
    });

    test("accepts valid tags", () => {
        expectSuccess(dockerImageTagSchema, "latest");
        expectSuccess(dockerImageTagSchema, "v1.2.3");
        expectSuccess(dockerImageTagSchema, "sha-abc123");
        expectSuccess(dockerImageTagSchema, "2024.01.15-alpine");
    });

    test("rejects tags exceeding 128 characters", () => {
        expectError(dockerImageTagSchema, "a".repeat(129), "128 characters");
    });

    test("rejects tags starting with non-alphanumeric", () => {
        expectError(dockerImageTagSchema, ".hidden", "start with an alphanumeric");
        expectError(dockerImageTagSchema, "-dash", "start with an alphanumeric");
    });

    test("rejects tags with invalid characters", () => {
        expectError(dockerImageTagSchema, "tag:bad", "start with an alphanumeric");
        expectError(dockerImageTagSchema, "tag bad", "start with an alphanumeric");
    });
});

describe("registrySecretNameSchema", () => {
    test("accepts empty/undefined (optional field)", () => {
        expectSuccess(registrySecretNameSchema, undefined);
        expectSuccess(registrySecretNameSchema, "");
    });

    test("accepts valid K8s secret names", () => {
        expectSuccess(registrySecretNameSchema, "my-registry-secret");
        expectSuccess(registrySecretNameSchema, "pull.secret.1");
        expectSuccess(registrySecretNameSchema, "a");
        expectSuccess(registrySecretNameSchema, "secret123");
    });

    test("rejects names exceeding 253 characters", () => {
        expectError(registrySecretNameSchema, "a".repeat(254), "253 characters");
    });

    test("rejects uppercase characters", () => {
        expectError(registrySecretNameSchema, "MySecret", "Kubernetes secret name");
    });

    test("rejects names starting/ending with non-alphanumeric", () => {
        expectError(registrySecretNameSchema, "-my-secret", "Kubernetes secret name");
        expectError(registrySecretNameSchema, "my-secret-", "Kubernetes secret name");
        expectError(registrySecretNameSchema, ".dot-start", "Kubernetes secret name");
    });

    test("rejects underscores (not valid in K8s names)", () => {
        expectError(registrySecretNameSchema, "my_secret", "Kubernetes secret name");
    });
});
