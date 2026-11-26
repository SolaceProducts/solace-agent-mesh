// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { type AddChange, useUnsavedAssignmentChanges } from "./useUnsavedAssignmentChanges";

const person = (id: string, moveFrom: AddChange["moveFrom"] = null): AddChange => ({ subject: { id, displayName: id, user: { email: null, isSystemUser: false } }, moveFrom });

describe("useUnsavedAssignmentChanges", () => {
    test("removing an unsaved add cancels it", () => {
        const { result } = renderHook(() => useUnsavedAssignmentChanges());
        act(() => result.current.add(person("u-a")));
        act(() => result.current.remove("u-a"));
        expect(result.current.changeCount).toBe(0);
        expect(result.current.isRemoved("u-a")).toBe(false);
    });
});
