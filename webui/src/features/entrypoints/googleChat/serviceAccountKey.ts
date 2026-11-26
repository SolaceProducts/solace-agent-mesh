// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const KEY_PROBLEMS = {
    notServiceAccount: "Service account key must be the JSON key file downloaded for a Google Cloud service account.",
    damagedPrivateKey: "Service account key's private_key is damaged. Paste the whole JSON key file, or download a fresh one.",
    foreignTokenUri: "Service account key names a token endpoint that is not Google's. Download a fresh key from the Google Cloud console.",
} as const;

const GOOGLE_TOKEN_URIS: ReadonlySet<string> = new Set(["https://oauth2.googleapis.com/token", "https://accounts.google.com/o/oauth2/token"]);

const DER_SEQUENCE_TAG = 0x30;
const DER_LONG_FORM_BIT = 0x80;
const DER_MAX_LENGTH_OCTETS = 4;
const PEM_BLOCK = /-----BEGIN ([^-\r\n]+)-----\r?\n([\s\S]*?)\r?\n-----END \1-----/;
const BASE64 = /^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/;

const KEY_FIELDS = ["type", "token_uri", "private_key", "client_email", "project_id"] as const;
const GATED_FIELDS: ReadonlySet<KeyField> = new Set(["type", "token_uri", "private_key"]);

type KeyField = (typeof KEY_FIELDS)[number];
type KeyShape = Partial<Record<KeyField, unknown>>;

const normalise = (text: string) => text.replaceAll("\r\n", "\n").trim();

function readFields(record: Record<string, unknown>): KeyShape | null {
    const shape: KeyShape = {};
    for (const field of KEY_FIELDS) {
        const spellings = Object.keys(record).filter(key => key.toLowerCase() === field);
        if (spellings.length > 1 && GATED_FIELDS.has(field)) return null;
        if (spellings.length > 0) shape[field] = record[spellings[0]];
    }
    return shape;
}

function parseKey(text: string): KeyShape | null {
    try {
        const parsed: unknown = JSON.parse(normalise(text));
        return typeof parsed === "object" && parsed !== null && !Array.isArray(parsed) ? readFields(parsed as Record<string, unknown>) : null;
    } catch {
        return null;
    }
}

function decodeBase64(encoded: string): Uint8Array | null {
    const compact = encoded.replaceAll(/\s+/g, "");
    if (compact === "" || !BASE64.test(compact)) return null;
    const binary = atob(compact);
    return Uint8Array.from(binary, char => char.codePointAt(0) ?? 0);
}

function hasWholeDerSequence(der: Uint8Array): boolean {
    if (der.length < 2 || der[0] !== DER_SEQUENCE_TAG) return false;
    const first = der[1];
    if (first < DER_LONG_FORM_BIT) return der.length === 2 + first;
    const octets = first & ~DER_LONG_FORM_BIT;
    if (octets === 0 || octets > DER_MAX_LENGTH_OCTETS || der.length < 2 + octets) return false;
    if (der[2] === 0) return false;
    const declared = Array.from(der.subarray(2, 2 + octets)).reduce((total, byte) => total * 256 + byte, 0);
    if (declared < DER_LONG_FORM_BIT) return false;
    return der.length === 2 + octets + declared;
}

function privateKeyIsWhole(privateKey: unknown): boolean {
    if (typeof privateKey !== "string") return false;
    const block = PEM_BLOCK.exec(privateKey);
    if (block === null) return false;
    const der = decodeBase64(block[2]);
    return der !== null && hasWholeDerSequence(der);
}

export function serviceAccountKeyProblem(text: string): string | null {
    const key = parseKey(text);
    if (key?.type !== "service_account") return KEY_PROBLEMS.notServiceAccount;
    if (!privateKeyIsWhole(key.private_key)) return KEY_PROBLEMS.damagedPrivateKey;
    const tokenUri = key.token_uri ?? "";
    if (tokenUri !== "" && (typeof tokenUri !== "string" || !GOOGLE_TOKEN_URIS.has(tokenUri))) return KEY_PROBLEMS.foreignTokenUri;
    return null;
}
