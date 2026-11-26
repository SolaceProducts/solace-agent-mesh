// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { HtmlRenderer } from "@/features/chat/preview/renderers/HTMLRenderer";

describe("HtmlRenderer", () => {
    test("keeps artifact HTML out of the app origin", () => {
        render(<HtmlRenderer content="<p>report</p>" setRenderError={vi.fn()} />);

        const sandbox = screen.getByTitle("HTML Preview").getAttribute("sandbox") ?? "";

        expect(sandbox.split(/\s+/)).toContain("allow-scripts");
        expect(sandbox.split(/\s+/)).not.toContain("allow-same-origin");
    });
});
