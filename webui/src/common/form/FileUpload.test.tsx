// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { createMockFile, createMockFileList } from "@/testing/helpers/files";

import { FileUpload } from "./FileUpload";

describe("FileUpload", () => {
    test("truncates a long selected file name and keeps the full name on hover", () => {
        const longName = `${"quarterly-financial-report-".repeat(8)}final.pdf`;
        const files = createMockFileList([createMockFile(longName, 10, "application/pdf")]);

        render(<FileUpload name="files" accept="*" value={files} onChange={vi.fn()} />);

        const fileName = screen.getByText(longName);
        expect(fileName).toHaveClass("truncate", "min-w-0");
        expect(fileName).toHaveAttribute("title", longName);
    });
});
