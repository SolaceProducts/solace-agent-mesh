// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { Project } from "@/types/projects";

import { selectOwnedProjectNames } from "./ownership";

const project = (id: string, name: string, userId: string): Project => ({
    id,
    name,
    userId,
    createdAt: "2024-01-01T00:00:00Z",
});

const projects: Project[] = [project("1", "Alpha", "Alice"), project("2", "Beta", "bob"), project("3", "Gamma", "alice")];

describe("selectOwnedProjectNames", () => {
    test("returns every project name when authorization is off, regardless of owner", () => {
        expect(selectOwnedProjectNames(projects, { username: "alice", configUseAuthorization: false, isSharingEnabled: true })).toEqual(["Alpha", "Beta", "Gamma"]);
    });

    test("returns every project name when sharing is off, regardless of owner", () => {
        expect(selectOwnedProjectNames(projects, { username: "alice", configUseAuthorization: true, isSharingEnabled: false })).toEqual(["Alpha", "Beta", "Gamma"]);
    });

    test("returns only the current user's projects, matched case-insensitively, when auth and sharing are on", () => {
        expect(selectOwnedProjectNames(projects, { username: "ALICE", configUseAuthorization: true, isSharingEnabled: true })).toEqual(["Alpha", "Gamma"]);
    });

    test("returns no names when auth and sharing are on but the username is missing", () => {
        expect(selectOwnedProjectNames(projects, { username: undefined, configUseAuthorization: true, isSharingEnabled: true })).toEqual([]);
    });

    test("returns no names when auth and sharing are on but the username is empty", () => {
        expect(selectOwnedProjectNames(projects, { username: "", configUseAuthorization: true, isSharingEnabled: true })).toEqual([]);
    });
});
