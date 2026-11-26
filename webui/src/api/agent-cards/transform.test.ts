// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import type { AgentCard } from "@/types/a2a";

import { transformAgentCard } from "./transform";

const makeCard = (extensions: AgentCard["capabilities"]["extensions"] = []): AgentCard =>
    ({
        name: "agent_test",
        description: "Test agent",
        url: "solace:topic",
        version: "1.0",
        protocolVersion: "0.3.0",
        defaultInputModes: ["text"],
        defaultOutputModes: ["text"],
        skills: [],
        capabilities: { extensions },
    }) as AgentCard;

const SAM_SKILLS_URI = "https://solace.com/a2a/extensions/sam/skills";

describe("transformAgentCard — sam/skills extension", () => {
    it("populates resolvedSkills from the sam/skills extension, sorted by name", () => {
        const card = makeCard([
            {
                uri: SAM_SKILLS_URI,
                params: {
                    skills: [
                        { name: "Web Research", description: "Search the web" },
                        { name: "Diagrams", description: "Draw diagrams" },
                    ],
                },
            },
        ]);
        expect(transformAgentCard(card).resolvedSkills).toEqual([
            { name: "Diagrams", description: "Draw diagrams" },
            { name: "Web Research", description: "Search the web" },
        ]);
    });

    it("defaults resolvedSkills to an empty array when the extension is absent", () => {
        expect(transformAgentCard(makeCard()).resolvedSkills).toEqual([]);
    });

    it("treats a non-array skills payload as empty rather than throwing", () => {
        const card = makeCard([{ uri: SAM_SKILLS_URI, params: { skills: "oops" } }]);
        expect(transformAgentCard(card).resolvedSkills).toEqual([]);
    });

    it("drops entries without a string name (one bad entry must not blank the card)", () => {
        const card = makeCard([
            {
                uri: SAM_SKILLS_URI,
                params: { skills: [{ description: "no name" }, { name: "Good", description: "ok" }] },
            },
        ]);
        expect(transformAgentCard(card).resolvedSkills).toEqual([{ name: "Good", description: "ok" }]);
    });
});

describe("transformAgentCard — sam/schemas extension", () => {
    const SCHEMAS_URI = "https://solace.com/a2a/extensions/sam/schemas";
    const schema = { type: "object", properties: { destination: { type: "string" } }, required: ["destination"] };

    it("lifts the declared input_schema onto the card", () => {
        const card = makeCard([{ uri: SCHEMAS_URI, params: { input_schema: schema, output_schema: { type: "object" } } }]);
        expect(transformAgentCard(card).inputSchema).toEqual(schema);
    });

    it("leaves inputSchema unset when the extension is absent or carries no input_schema", () => {
        expect(transformAgentCard(makeCard()).inputSchema).toBeUndefined();
        expect(transformAgentCard(makeCard([{ uri: SCHEMAS_URI, params: { output_schema: {} } }])).inputSchema).toBeUndefined();
    });

    it.each([
        ["a string", "oops"],
        ["an array", [1]],
        ["null", null],
    ])("ignores an input_schema that is %s", (_label, value) => {
        expect(transformAgentCard(makeCard([{ uri: SCHEMAS_URI, params: { input_schema: value } }])).inputSchema).toBeUndefined();
    });
});

describe("transformAgentCard — sam/remote-protocol extension", () => {
    const REMOTE_PROTOCOL_URI = "https://solace.com/a2a/extensions/sam/remote-protocol";

    it("reads the version, binding, older-version flag and signature outcome the proxy reports", () => {
        const card = makeCard([{ uri: REMOTE_PROTOCOL_URI, params: { protocol_version: "0.3", protocol_binding: "JSONRPC", deprecated: true, card_signature: "refused" } }]);
        expect(transformAgentCard(card).remoteProtocol).toEqual({ selectedVersion: "v0_3", binding: "jsonrpc", deprecated: true, cardSignature: "refused" });
        const rest = makeCard([{ uri: REMOTE_PROTOCOL_URI, params: { protocol_version: "1.0", protocol_binding: "HTTP+JSON", deprecated: false, card_signature: "verified" } }]);
        expect(transformAgentCard(rest).remoteProtocol).toEqual({ selectedVersion: "v1_0", binding: "http_json", deprecated: false, cardSignature: "verified" });
    });

    it("reads a report with no signature outcome as not checked", () => {
        const card = makeCard([{ uri: REMOTE_PROTOCOL_URI, params: { protocol_version: "1.0", protocol_binding: "JSONRPC", deprecated: false } }]);
        expect(transformAgentCard(card).remoteProtocol?.cardSignature).toBe("unchecked");
    });

    it("reads no report from a card without the extension", () => {
        expect(transformAgentCard(makeCard()).remoteProtocol).toBeUndefined();
    });

    it.each([
        ["an unknown version", { protocol_version: "2.0", protocol_binding: "JSONRPC" }],
        ["an unknown binding", { protocol_version: "1.0", protocol_binding: "GRPC" }],
        ["a version that names an object property", { protocol_version: "constructor", protocol_binding: "JSONRPC" }],
        ["a binding that names an object property", { protocol_version: "1.0", protocol_binding: "toString" }],
        ["a prototype key", { protocol_version: "__proto__", protocol_binding: "__proto__" }],
        ["a number for the version", { protocol_version: 0.3, protocol_binding: "JSONRPC" }],
        ["an array for the version", { protocol_version: ["1.0"], protocol_binding: "JSONRPC" }],
        ["an unknown signature outcome", { protocol_version: "1.0", protocol_binding: "JSONRPC", card_signature: "maybe" }],
        ["a signature outcome that names an object property", { protocol_version: "1.0", protocol_binding: "JSONRPC", card_signature: "constructor" }],
    ])("reads no report from %s", (_, params) => {
        expect(transformAgentCard(makeCard([{ uri: REMOTE_PROTOCOL_URI, params }])).remoteProtocol).toBeUndefined();
    });

    it.each(["unsigned", "unchecked"] as const)("reads a %s outcome as it is", cardSignature => {
        const card = makeCard([{ uri: REMOTE_PROTOCOL_URI, params: { protocol_version: "1.0", protocol_binding: "JSONRPC", card_signature: cardSignature } }]);
        expect(transformAgentCard(card).remoteProtocol?.cardSignature).toBe(cardSignature);
    });

    it("reads an empty signature outcome as not checked", () => {
        const card = makeCard([{ uri: REMOTE_PROTOCOL_URI, params: { protocol_version: "1.0", protocol_binding: "JSONRPC", card_signature: "" } }]);
        expect(transformAgentCard(card).remoteProtocol?.cardSignature).toBe("unchecked");
    });
});
