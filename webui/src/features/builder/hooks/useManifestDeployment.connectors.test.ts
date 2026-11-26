// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { BuildManifest } from "./useBuilderManifest";
import { expandConnectorsToConsumers } from "./useManifestDeployment";

type Comp = NonNullable<BuildManifest["components"]>[number];

const comp = (name: string, type: string, extra: Partial<Comp> = {}): Comp => ({ filename: `${name}.yaml`, type, name, status: "validated", origin: "session", ...extra }) as Comp;

describe("expandConnectorsToConsumers", () => {
    const components = [comp("db", "connector"), comp("cache", "connector"), comp("Reader", "agent", { connectors: ["db"] }), comp("Writer", "agent", { depends_on: ["cache"] }), comp("Lonely", "agent", { connectors: [] })];

    test("an agent bound through connectors is pulled in", () => {
        expect(expandConnectorsToConsumers(["db"], components)).toContain("Reader");
    });

    test("an agent bound through depends_on is pulled in too", () => {
        expect(expandConnectorsToConsumers(["cache"], components)).toContain("Writer");
    });

    test("agents bound to a connector nothing changed are left alone", () => {
        const expanded = expandConnectorsToConsumers(["db"], components);
        expect(expanded).not.toContain("Writer");
        expect(expanded).not.toContain("Lonely");
    });

    test("the changed components themselves are kept", () => {
        expect(expandConnectorsToConsumers(["db", "Reader"], components)).toEqual(expect.arrayContaining(["db", "Reader"]));
    });

    test("a changed AGENT does not drag in others", () => {
        expect(expandConnectorsToConsumers(["Reader"], components)).toEqual(["Reader"]);
    });

    test("nothing changed expands to nothing", () => {
        expect(expandConnectorsToConsumers([], components)).toEqual([]);
    });

    test("a peer agent named in depends_on is not mistaken for a connector", () => {
        const withPeers = [...components, comp("Upstream", "agent"), comp("Downstream", "agent", { depends_on: ["Upstream"] })];
        expect(expandConnectorsToConsumers(["Upstream"], withPeers)).toEqual(["Upstream"]);
    });

    test("a workflow binding a changed connector is not pulled in", () => {
        const withWorkflow = [...components, comp("Pipeline", "workflow", { depends_on: ["db"] })];
        expect(expandConnectorsToConsumers(["db"], withWorkflow)).not.toContain("Pipeline");
    });

    test("a binding spelled differently from the connector component still expands", () => {
        const spelled = [comp("my_db", "connector"), comp("Reader2", "agent", { connectors: ["My DB"] })];
        expect(expandConnectorsToConsumers(["my_db"], spelled)).toContain("Reader2");
    });
});
