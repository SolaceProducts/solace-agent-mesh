// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { documentCitation, multipleDocumentCitations } from "@/testing/data/citations";

import { TextWithCitations } from "./TextWithCitations";

describe("TextWithCitations edge cases", () => {
    test("renders nothing when citations array is empty (no badges)", () => {
        const { container } = render(<TextWithCitations text="hello world" citations={[]} />);
        expect(container.querySelectorAll("button")).toHaveLength(0);
    });

    test("deduplicates citations by sourceId+type when rendering bundled marker", async () => {
        const text = "Foo bar baz [[cite:idx0r0,cite:idx0r0,cite:idx0r1]]";
        render(<TextWithCitations text={text} citations={[documentCitation, documentCitation, multipleDocumentCitations[1]]} />);

        const badge = await screen.findByRole("button");
        expect(badge).toHaveTextContent("+1");
    });

    test("renders a single citation as a single badge (no +N suffix) when only one unique citation matches", async () => {
        const text = "Foo bar [[cite:idx0r0]]";
        render(<TextWithCitations text={text} citations={[documentCitation]} />);

        const badge = await screen.findByRole("button");
        expect(badge).not.toHaveTextContent("+");
    });
});
