// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import yaml from "js-yaml";
import { describe, expect, test } from "vitest";

import { BuildManifestSchema } from "./useBuilderManifest";

const DECISION_MANIFEST = `
name: triage-build
components:
  - filename: triage.yaml
    type: workflow
    name: Triage
    nodes:
      - id: triage
        type: decision
        questions:
          department:
            type: choice
            instructions: Which team owns the ticket?
            criteria:
              billing: Payments and invoices
              tech: Bugs and outages
          urgent:
            type: boolean
            instructions: Is the customer blocked?
        route_on: department
        routes:
          billing: billing_desk
          tech: tech_desk
        default: tech_desk
        min_confidence: 0.8
        escalate: human_review
        escalation_mode: handoff
        on_provider_error: escalate
      - id: billing_desk
        type: agent
        agent_name: Billing
        depends_on: [triage]
`;

describe("BuildManifestSchema decision nodes", () => {
    test("parses a decision node's questions, routing and escalation fields from the manifest", () => {
        const parsed = BuildManifestSchema.safeParse(yaml.load(DECISION_MANIFEST));

        expect(parsed.success).toBe(true);
        const decision = parsed.data?.components?.[0].nodes?.[0];
        expect(decision).toMatchObject({
            type: "decision",
            route_on: "department",
            routes: { billing: "billing_desk", tech: "tech_desk" },
            default: "tech_desk",
            min_confidence: 0.8,
            escalate: "human_review",
            escalation_mode: "handoff",
            on_provider_error: "escalate",
        });
        expect(decision?.questions).toEqual({
            department: { type: "choice", instructions: "Which team owns the ticket?", criteria: { billing: "Payments and invoices", tech: "Bugs and outages" } },
            urgent: { type: "boolean", instructions: "Is the customer blocked?" },
        });
    });

    test("keeps the manifest when a question is still missing its type", () => {
        const doc = { name: "b", components: [{ filename: "w.yaml", type: "workflow", name: "W", nodes: [{ id: "d", type: "decision", questions: { department: { instructions: "Which team?" } } }] }] };

        expect(BuildManifestSchema.safeParse(doc).success).toBe(true);
    });
});

describe("BuildManifestSchema null fields", () => {
    const parse = (doc: string) => BuildManifestSchema.safeParse(yaml.load(doc, { json: true }));

    test("reads full_validation_passed: null as absent", () => {
        const parsed = parse("name: sys\nfull_validation_passed: null\ncomponents: []\n");

        expect(parsed.success).toBe(true);
        expect(parsed.data?.full_validation_passed).toBeUndefined();
    });

    test("reads a null in every optional field, at the root, on components, on workflow nodes and on env vars, as absent", () => {
        const parsed = parse(`
name: sys
display_name: null
description:
architecture: ~
full_validation_passed: null
risks: null
loaded_for_edit: null
env_vars:
  - name: TOKEN
    description: null
    required: null
    default: null
components:
  - filename: a.yaml
    type: agent
    name: A
    description: null
    id: null
    deployed_version: null
    status: null
    origin: null
    depends_on: null
    connectors: null
    tools: null
    nodes: null
    gateway_type: null
    icon: null
  - filename: w.yaml
    type: workflow
    name: W
    nodes:
      - id: n1
        type: switch
        depends_on: null
        agent_name: null
        workflow_name: null
        tool_name: null
        cases: null
        default: null
        node: null
        items: null
        condition: null
        max_iterations: null
        delay: null
`);

        expect(parsed.success).toBe(true);
        const manifest = parsed.data!;
        expect(manifest.name).toBe("sys");
        expect(manifest.components).toHaveLength(2);
        expect(manifest.components?.[0]).toEqual({ filename: "a.yaml", type: "agent", name: "A" });
        expect(manifest.components?.[1].nodes?.[0]).toEqual({ id: "n1", type: "switch" });
        expect(manifest.env_vars?.[0]).toEqual({ name: "TOKEN" });
        expect(manifest.display_name).toBeUndefined();
    });

    test("keeps the values next to a null, and drops a null the schema does not name", () => {
        const parsed = parse("name: sys\nfull_validation_passed: null\ndisplay_name: Sys\nfuture_field: null\ncomponents:\n  - filename: a.yaml\n    type: agent\n    name: A\n    status: validated\n    description: null\n");

        expect(parsed.data).toEqual({ name: "sys", display_name: "Sys", components: [{ filename: "a.yaml", type: "agent", name: "A", status: "validated" }] });
    });

    test("dumps back without the nulls, so a write built on the parsed manifest does not carry them over", () => {
        const parsed = parse("name: sys\nfull_validation_passed: null\ncomponents:\n  - filename: a.yaml\n    type: agent\n    name: A\n    deployed_version: null\n");

        expect(parsed.success).toBe(true);
        expect(yaml.dump(parsed.data)).not.toMatch(/null|full_validation_passed|deployed_version/);
    });

    test("keeps a timestamp or binary value in an unlisted field intact through parse and dump", () => {
        const parsed = parse("name: sys\ncreated: 2026-10-19\nblob: !!binary aGk=\ncomponents:\n  - filename: a.yaml\n    type: agent\n    name: A\n    built_at: 2026-10-19T10:00:00Z\n    gone: null\n");

        expect(parsed.success).toBe(true);
        const dumped = yaml.dump(parsed.data);
        expect(dumped).toContain("built_at: 2026-10-19T10:00:00.000Z");
        expect(dumped).toContain("created: 2026-10-19T00:00:00.000Z");
        expect((parsed.data as { blob?: unknown }).blob).toBeInstanceOf(Uint8Array);
        expect(dumped).not.toContain("gone");
        expect(dumped).not.toContain("{}");
    });

    test("still rejects a null in a required field and a wrong type in an optional one", () => {
        expect(parse("name: null\n").success).toBe(false);
        expect(parse("name: sys\ncomponents:\n  - filename: a.yaml\n    type: agent\n    name: null\n").success).toBe(false);
        expect(parse('name: sys\nfull_validation_passed: "yes"\n').success).toBe(false);
        expect(parse("name: sys\ncomponents:\n  - filename: a.yaml\n    type: agent\n    name: A\n    tools: [null]\n").success).toBe(false);
    });
});

describe("BuildManifestSchema validation stamp", () => {
    test("keeps the recorded component versions", () => {
        const parsed = BuildManifestSchema.safeParse({ name: "b", full_validation_passed: true, full_validation_versions: { "a.yaml": 2 } });

        expect(parsed.data?.full_validation_versions).toEqual({ "a.yaml": 2 });
    });

    test.each([
        ["a string", "oops"],
        ["a list", [1, 2]],
        ["a map with a non-number version", { "a.yaml": "two" }],
    ])("reads %s as no recorded versions without making the manifest unreadable", (_label, malformed) => {
        const parsed = BuildManifestSchema.safeParse({ name: "b", full_validation_passed: true, full_validation_versions: malformed });

        expect(parsed.success).toBe(true);
        expect(parsed.data?.full_validation_versions).toBeUndefined();
        expect(parsed.data?.full_validation_passed).toBe(true);
    });
});
