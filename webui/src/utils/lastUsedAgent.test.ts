// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { clearLastUsedAgentName, getLastUsedAgentName, setLastUsedAgentName } from "./lastUsedAgent";

describe("lastUsedAgent", () => {
    beforeEach(() => {
        localStorage.clear();
        vi.spyOn(console, "warn").mockImplementation(() => {});
    });

    afterEach(() => {
        vi.restoreAllMocks();
    });

    it("round-trips an agent name for a user", () => {
        setLastUsedAgentName("alice", "SupportAgent");
        expect(getLastUsedAgentName("alice")).toBe("SupportAgent");
    });

    it("stores under a user-namespaced key as a bare string", () => {
        setLastUsedAgentName("alice", "SupportAgent");
        expect(localStorage.getItem("sam-last-used-agent:alice")).toBe("SupportAgent");
    });

    it("returns null when nothing is stored for the user", () => {
        expect(getLastUsedAgentName("alice")).toBeNull();
    });

    it("isolates users from one another", () => {
        setLastUsedAgentName("alice", "SupportAgent");
        expect(getLastUsedAgentName("bob")).toBeNull();
    });

    it("one user's write does not disturb another's", () => {
        setLastUsedAgentName("alice", "SupportAgent");
        setLastUsedAgentName("bob", "SalesAgent");
        expect(getLastUsedAgentName("alice")).toBe("SupportAgent");
        expect(getLastUsedAgentName("bob")).toBe("SalesAgent");
    });

    it("overwrites the previous pick for the same user", () => {
        setLastUsedAgentName("alice", "SupportAgent");
        setLastUsedAgentName("alice", "SalesAgent");
        expect(getLastUsedAgentName("alice")).toBe("SalesAgent");
    });

    it("persists for the anonymous user", () => {
        setLastUsedAgentName("anonymous", "SupportAgent");
        expect(getLastUsedAgentName("anonymous")).toBe("SupportAgent");
    });

    it("ignores an empty user id", () => {
        setLastUsedAgentName("", "SupportAgent");
        expect(localStorage.length).toBe(0);
        expect(getLastUsedAgentName("")).toBeNull();
    });

    it("ignores an empty agent name", () => {
        setLastUsedAgentName("alice", "");
        expect(localStorage.length).toBe(0);
    });

    it("treats an empty stored value as absent", () => {
        localStorage.setItem("sam-last-used-agent:alice", "");
        expect(getLastUsedAgentName("alice")).toBeNull();
    });

    it("clear removes only that user's entry", () => {
        setLastUsedAgentName("alice", "SupportAgent");
        setLastUsedAgentName("bob", "SalesAgent");
        clearLastUsedAgentName("alice");
        expect(getLastUsedAgentName("alice")).toBeNull();
        expect(getLastUsedAgentName("bob")).toBe("SalesAgent");
    });

    it("read survives a throwing localStorage", () => {
        vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
            throw new Error("SecurityError");
        });
        expect(getLastUsedAgentName("alice")).toBeNull();
    });

    it("write survives a throwing localStorage", () => {
        vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
            throw new Error("QuotaExceededError");
        });
        expect(() => setLastUsedAgentName("alice", "SupportAgent")).not.toThrow();
    });

    it("clear survives a throwing localStorage", () => {
        vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => {
            throw new Error("SecurityError");
        });
        expect(() => clearLastUsedAgentName("alice")).not.toThrow();
    });
});
