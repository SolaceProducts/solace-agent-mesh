// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { withGeneratedKey } from "@/forms/testing/generatedKey";

import { KEY_PROBLEMS, serviceAccountKeyProblem } from "./serviceAccountKey";

const keyFile = (extra = "", privateKey = "{{PRIVATE_KEY_PEM}}") => `{"type":"service_account","private_key":"${privateKey}","client_email":"a@p.iam.gserviceaccount.com"${extra}}`;

const INVALID_KEYS: { note: string; input: string; want: string }[] = [
    { note: "not JSON", input: "not json", want: KEY_PROBLEMS.notServiceAccount },
    { note: "wrong credential type", input: '{"type":"authorized_user"}', want: KEY_PROBLEMS.notServiceAccount },
    { note: "private_key cut to 60 characters", input: keyFile("", "{{PRIVATE_KEY_PEM_TRUNCATED_60}}"), want: KEY_PROBLEMS.damagedPrivateKey },
    { note: "private_key is the base64 body without PEM armour", input: keyFile("", "{{PRIVATE_KEY_PEM_NO_ARMOUR}}"), want: KEY_PROBLEMS.damagedPrivateKey },
    { note: "private_key is missing its END line", input: keyFile("", "{{PRIVATE_KEY_PEM_NO_END}}"), want: KEY_PROBLEMS.damagedPrivateKey },
    { note: "private_key BEGIN and END labels differ", input: keyFile("", "{{PRIVATE_KEY_PEM_LABEL_MISMATCH}}"), want: KEY_PROBLEMS.damagedPrivateKey },
    { note: "token_uri spelled in another case names a foreign endpoint", input: keyFile(',"Token_URI":"https://evil.example/token"'), want: KEY_PROBLEMS.foreignTokenUri },
    { note: "token_uri is not Google's", input: keyFile(',"token_uri":"https://evil.example/token"'), want: KEY_PROBLEMS.foreignTokenUri },
];

const VALID_KEYS: { note: string; input: string }[] = [
    { note: "minimal key", input: keyFile() },
    { note: "Google's token_uri", input: keyFile(',"token_uri":"https://oauth2.googleapis.com/token"') },
];

const VALID_TEMPLATE = '{"type":"service_account","project_id":"proj-1","private_key":"{{PRIVATE_KEY_PEM}}","client_email":"a@p.iam.gserviceaccount.com"}';

const label = (index: number, note?: string) => `service_account_key case ${index} (${note ?? "no note"})`;

describe("serviceAccountKeyProblem", () => {
    test("refuses each bad key and accepts each good one", async () => {
        for (const [index, c] of INVALID_KEYS.entries()) {
            expect(serviceAccountKeyProblem(await withGeneratedKey(c.input)), label(index, c.note)).toBe(c.want);
        }
        for (const [index, c] of VALID_KEYS.entries()) {
            expect(serviceAccountKeyProblem(await withGeneratedKey(c.input)), label(index, c.note)).toBeNull();
        }
    });

    test("gives the Go messages in the Go order", async () => {
        expect(serviceAccountKeyProblem("not json")).toBe("Service account key must be the JSON key file downloaded for a Google Cloud service account.");
        expect(serviceAccountKeyProblem('{"type":"authorized_user"}')).toBe("Service account key must be the JSON key file downloaded for a Google Cloud service account.");
        expect(serviceAccountKeyProblem('{"type":"service_account","private_key":"nope","token_uri":"https://evil.example/token"}')).toBe("Service account key's private_key is damaged. Paste the whole JSON key file, or download a fresh one.");
        const foreign = await withGeneratedKey('{"type":"service_account","private_key":"{{PRIVATE_KEY_PEM}}","token_uri":"https://evil.example/token"}');
        expect(serviceAccountKeyProblem(foreign)).toBe("Service account key names a token endpoint that is not Google's. Download a fresh key from the Google Cloud console.");
        const legacy = await withGeneratedKey('{"type":"service_account","private_key":"{{PRIVATE_KEY_PEM}}","token_uri":"https://accounts.google.com/o/oauth2/token"}');
        expect(serviceAccountKeyProblem(legacy)).toBeNull();
    });

    test("accepts CRLF, surrounding whitespace and a trailing newline", async () => {
        const valid = await withGeneratedKey(VALID_TEMPLATE);
        const pretty = JSON.stringify(JSON.parse(valid), null, 2);
        expect(serviceAccountKeyProblem(`  \n${valid}\n`)).toBeNull();
        expect(serviceAccountKeyProblem(`${pretty.replaceAll("\n", "\r\n")}\r\n`)).toBeNull();
        const crlfPem = valid.replaceAll("\\n", "\\r\\n");
        expect(serviceAccountKeyProblem(crlfPem)).toBeNull();
    });

    test("refuses a gated field spelled in two cases, and reads a single other-case spelling like Go", async () => {
        const valid = await withGeneratedKey(VALID_TEMPLATE);
        const parsed = JSON.parse(valid) as Record<string, string>;
        const twice = (extra: Record<string, string>) => serviceAccountKeyProblem(JSON.stringify({ ...parsed, ...extra }));
        expect(twice({ Type: "authorized_user" })).toBe(KEY_PROBLEMS.notServiceAccount);
        expect(twice({ PRIVATE_KEY: "x" })).toBe(KEY_PROBLEMS.notServiceAccount);
        expect(twice({ token_uri: "https://oauth2.googleapis.com/token", Token_URI: "https://evil.example/token" })).toBe(KEY_PROBLEMS.notServiceAccount);
        const { type: _type, ...rest } = parsed;
        expect(serviceAccountKeyProblem(JSON.stringify({ ...rest, TYPE: "service_account" }))).toBeNull();
    });

    test("refuses non-minimal DER lengths and accepts the minimal form of the same bytes", () => {
        const keyWith = (bytes: number[]) => {
            const body = btoa(String.fromCharCode(...bytes));
            return JSON.stringify({ type: "service_account", private_key: `-----BEGIN PRIVATE KEY-----\n${body}\n-----END PRIVATE KEY-----\n` });
        };
        const content = [0x02, 0x01, 0x00];
        expect(serviceAccountKeyProblem(keyWith([0x30, 0x03, ...content]))).toBeNull();
        expect(serviceAccountKeyProblem(keyWith([0x30, 0x81, 0x03, ...content]))).toBe(KEY_PROBLEMS.damagedPrivateKey);
        expect(serviceAccountKeyProblem(keyWith([0x30, 0x82, 0x00, 0x03, ...content]))).toBe(KEY_PROBLEMS.damagedPrivateKey);
    });

    test("never echoes input", async () => {
        const pem = JSON.parse(await withGeneratedKey('{"k":"{{PRIVATE_KEY_PEM}}"}')).k as string;
        const keyBodyFragment = pem
            .replace(/-----[^-]+-----/g, "")
            .trim()
            .slice(0, 16);
        for (const [index, c] of INVALID_KEYS.entries()) {
            const message = serviceAccountKeyProblem((await withGeneratedKey(c.input)).trim()) ?? "";
            expect(message, label(index, c.note)).not.toContain("a@p.iam.gserviceaccount.com");
            expect(message, label(index, c.note)).not.toContain(keyBodyFragment);
        }
    });
});
