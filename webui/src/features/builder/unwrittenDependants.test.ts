// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { BuildManifest } from "./hooks/useBuilderManifest";
import { dependantsOfUnwritten, type ReadComponentConfig } from "./unwrittenDependants";

type Comp = NonNullable<BuildManifest["components"]>[number];

const comp = (name: string, type: string, extra: Record<string, unknown> = {}): Comp => ({ filename: `${name}.yaml`, type, name, status: "validated", origin: "session", ...extra }) as Comp;

const agentYaml = (skills: string[]) => `
apps:
  - name: A
    app_config:
      agent_name: A
      description: An agent with a sufficiently long description.
      instruction: |
        ${"x".repeat(120)}
      skills:
${skills.map(name => `        - name: ${name}`).join("\n")}
`;

const configs =
    (byName: Record<string, string>): ReadComponentConfig =>
    async c =>
        byName[c.name] ?? null;
const noConfigs: ReadComponentConfig = async () => agentYaml([]);

describe("dependantsOfUnwritten — what uses a component that was not written", () => {
    test.each([
        ["depends_on", comp("A", "agent", { depends_on: ["S"] })],
        ["the manifest's skills list", comp("A", "agent", { skills: ["S"] })],
        ["a connector list", comp("A", "agent", { connectors: ["S"] })],
        ["a differently spelled name", comp("A", "agent", { depends_on: ["s_"] })],
    ])("finds an agent that names it through %s", async (_label, agent) => {
        expect(await dependantsOfUnwritten(["S"], [comp("S", "skill"), agent], noConfigs)).toEqual(["A"]);
    });

    test("finds an agent whose manifest lists the skill as a { name } entry", async () => {
        expect(await dependantsOfUnwritten(["S"], [comp("S", "skill"), comp("A", "agent", { skills: [{ name: "S" }] })], noConfigs)).toEqual(["A"]);
    });

    test.each(["workflow", "gateway", "a2a_agent", "skill", "connector"])("does not read a %s as if it were an agent's config", async type => {
        const reads: string[] = [];
        const read: ReadComponentConfig = async c => {
            reads.push(c.name);
            return agentYaml(["S"]);
        };

        expect(await dependantsOfUnwritten(["S"], [comp("S", "skill"), comp("X", type)], read)).toEqual([]);
        expect(reads).toEqual([]);
    });

    test("finds an agent that binds it only through its own config", async () => {
        const read = configs({ A: agentYaml(["S"]) });

        expect(await dependantsOfUnwritten(["S"], [comp("S", "skill"), comp("A", "agent")], read)).toEqual(["A"]);
    });

    test("finds an agent that binds it under a differently spelled name in its config", async () => {
        const read = configs({ A: agentYaml(["Haiku-Style"]) });

        expect(await dependantsOfUnwritten(["haiku_style"], [comp("haiku_style", "skill"), comp("A", "agent")], read)).toEqual(["A"]);
    });

    test("follows the chain: a workflow that depends on a withheld agent is withheld too", async () => {
        const components = [comp("S", "skill"), comp("A", "agent", { depends_on: ["S"] }), comp("W", "workflow", { depends_on: ["A"] }), comp("G", "gateway", { depends_on: ["W"] })];

        expect(await dependantsOfUnwritten(["S"], components, noConfigs)).toEqual(["A", "W", "G"]);
    });

    test("follows the chain whichever order the components are listed in", async () => {
        const components = [comp("G", "gateway", { depends_on: ["W"] }), comp("W", "workflow", { depends_on: ["A"] }), comp("A", "agent", { depends_on: ["S"] }), comp("S", "skill")];

        expect(await dependantsOfUnwritten(["S"], components, noConfigs)).toEqual(["A", "W", "G"]);
    });

    test.each([
        ["nothing listed", comp("B", "agent")],
        ["an empty list", comp("B", "agent", { depends_on: [] })],
        ["another skill that was written", comp("B", "agent", { depends_on: ["T"] })],
        ["a connector that was written", comp("B", "agent", { connectors: ["db"] })],
        ["a name that only contains it", comp("B", "agent", { depends_on: ["S2"] })],
    ])("leaves alone an agent with %s", async (_label, agent) => {
        const components = [comp("S", "skill"), comp("T", "skill"), comp("db", "connector"), agent];

        expect(await dependantsOfUnwritten(["S"], components, noConfigs)).toEqual([]);
    });

    test("leaves alone an agent whose own config binds a different skill", async () => {
        const read = configs({ B: agentYaml(["T"]) });

        expect(await dependantsOfUnwritten(["S"], [comp("S", "skill"), comp("T", "skill"), comp("B", "agent")], read)).toEqual([]);
    });

    test.each([[[null, 5, { name: 1 }, {}]], [[""]]])("ignores a skills entry that names nothing: %j", async entries => {
        expect(await dependantsOfUnwritten(["S"], [comp("S", "skill"), comp("A", "agent", { skills: entries })], noConfigs)).toEqual([]);
    });

    test("does not look at a platform component, which this save neither writes nor deploys", async () => {
        const read: ReadComponentConfig = async () => agentYaml(["S"]);

        expect(await dependantsOfUnwritten(["S"], [comp("S", "skill"), comp("P", "agent", { origin: "platform", depends_on: ["S"] })], read)).toEqual([]);
    });

    test("does not read a toolset as if it were an agent's config", async () => {
        const reads: string[] = [];
        const read: ReadComponentConfig = async c => {
            reads.push(c.name);
            return null;
        };

        await dependantsOfUnwritten(["S"], [comp("S", "skill"), comp("T", "toolset")], read);

        expect(reads).toEqual([]);
    });

    const unreadable: Array<[string, ReadComponentConfig]> = [
        [
            "throws",
            async () => {
                throw new Error("expired");
            },
        ],
        ["comes back empty", async () => null],
    ];

    test.each(unreadable)("an agent whose config %s may bind the skill, so it is withheld", async (_label, read) => {
        expect(await dependantsOfUnwritten(["S"], [comp("S", "skill"), comp("A", "agent")], read)).toEqual(["A"]);
    });

    test.each(unreadable)("a component of another type whose config %s is left alone, as its config is never read", async (_label, read) => {
        expect(await dependantsOfUnwritten(["S"], [comp("S", "skill"), comp("W", "workflow"), comp("G", "gateway")], read)).toEqual([]);
    });

    test("an unreadable agent drags along what depends on it", async () => {
        const read: ReadComponentConfig = async () => null;

        expect(await dependantsOfUnwritten(["S"], [comp("S", "skill"), comp("A", "agent"), comp("W", "workflow", { depends_on: ["A"] })], read)).toEqual(["A", "W"]);
    });

    test("an unreadable config does not hide what the manifest lists", async () => {
        const read: ReadComponentConfig = async () => {
            throw new Error("expired");
        };

        expect(await dependantsOfUnwritten(["S"], [comp("S", "skill"), comp("A", "agent", { depends_on: ["S"] })], read)).toEqual(["A"]);
    });

    test("names each component once, and not the unwritten one itself", async () => {
        const components = [comp("S", "skill"), comp("A", "agent", { depends_on: ["S"], skills: ["S"] })];

        expect(await dependantsOfUnwritten(["S"], components, configs({ A: agentYaml(["S"]) }))).toEqual(["A"]);
    });

    test("with nothing unwritten there is nothing to withhold", async () => {
        expect(await dependantsOfUnwritten([], [comp("A", "agent", { depends_on: ["S"] })], noConfigs)).toEqual([]);
    });
});
