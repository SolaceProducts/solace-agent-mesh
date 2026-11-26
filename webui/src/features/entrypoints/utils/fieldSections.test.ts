// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { DEFAULT_SECTION_TITLE, type FieldEntry, groupFieldsIntoSections } from "./fieldSections";

const field = (name: string, section?: string): FieldEntry => [name, { label: name, type: "string", section }];

const titlesAndNames = (fields: FieldEntry[]) => groupFieldsIntoSections(fields).map(section => [section.title, section.fields.map(([name]) => name)]);

describe("groupFieldsIntoSections", () => {
    it("puts unsectioned fields under the default section and sectioned ones under their own heading", () => {
        expect(titlesAndNames([field("a"), field("b"), field("c", "Identity"), field("d", "Identity")])).toEqual([
            [DEFAULT_SECTION_TITLE, ["a", "b"]],
            ["Identity", ["c", "d"]],
        ]);
    });

    it("merges an unsectioned field that sorts between sectioned ones into the single default section", () => {
        expect(titlesAndNames([field("a"), field("b", "Identity"), field("c"), field("d", "Identity")])).toEqual([
            [DEFAULT_SECTION_TITLE, ["a", "c"]],
            ["Identity", ["b", "d"]],
        ]);
    });

    it("keeps the default section first when the first field is sectioned", () => {
        expect(titlesAndNames([field("a", "Identity"), field("b")])).toEqual([
            [DEFAULT_SECTION_TITLE, ["b"]],
            ["Identity", ["a"]],
        ]);
    });

    it("keeps an empty default section when every field is sectioned, so the prerequisites still have their heading", () => {
        expect(titlesAndNames([field("a", "Identity")])).toEqual([
            [DEFAULT_SECTION_TITLE, []],
            ["Identity", ["a"]],
        ]);
    });

    it("returns just the default section for no fields", () => {
        expect(titlesAndNames([])).toEqual([[DEFAULT_SECTION_TITLE, []]]);
    });
});
