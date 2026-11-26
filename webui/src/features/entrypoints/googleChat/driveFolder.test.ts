// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { driveFolderIdFromInput, driveFolderProblem } from "./driveFolder";

const ID = "0AB1cD2eF3gH4iJ5kL";

describe("driveFolderIdFromInput", () => {
    test.each([
        `https://drive.google.com/drive/folders/${ID}`,
        `https://drive.google.com/drive/u/1/folders/${ID}`,
        `https://drive.google.com/drive/mobile/folders/${ID}`,
        `https://drive.google.com/drive/folders/${ID}?usp=sharing`,
        `https://drive.google.com/drive/folders/${ID}?resourcekey=0-abc`,
        `https://drive.google.com/open?id=${ID}`,
        `https://drive.google.com/open?usp=sharing&id=${ID}`,
        `drive.google.com/drive/folders/${ID}`,
        `  ${ID}  `,
    ])("takes the id from %s", input => {
        expect(driveFolderIdFromInput(input)).toBe(ID);
    });
});

describe("driveFolderProblem", () => {
    test("empty is allowed, since the folder is optional", () => {
        expect(driveFolderProblem("")).toBeNull();
        expect(driveFolderProblem("   ")).toBeNull();
    });

    test("a folder link or a bare id is accepted", () => {
        expect(driveFolderProblem(`https://drive.google.com/drive/u/0/folders/${ID}?usp=sharing`)).toBeNull();
        expect(driveFolderProblem(ID)).toBeNull();
        expect(driveFolderProblem("abcdefghij")).toBeNull();
    });

    test.each([
        ["a file", `https://drive.google.com/file/d/${ID}/view`, "That's a link to a file. Paste the link of the folder where the files go."],
        ["a Google Doc", `https://docs.google.com/document/d/${ID}/edit`, "That's a link to a file. Paste the link of the folder where the files go."],
        ["My Drive", "https://drive.google.com/drive/my-drive", "Open the folder itself in Drive and paste its link."],
        ["a folder link that was cut short", "https://drive.google.com/drive/folders/abc", "Paste the folder's link from Drive, or its ID."],
        ["an id one character too short", "abcdefghi", "Paste the folder's link from Drive, or its ID."],
        ["some other text", "my team folder", "Paste the folder's link from Drive, or its ID."],
        ["a link elsewhere", `https://example.com/drive/folders/${ID}`, "Paste the folder's link from Drive, or its ID."],
    ])("%s gets its own message", (_, input, want) => {
        expect(driveFolderProblem(input)).toBe(want);
    });
});
