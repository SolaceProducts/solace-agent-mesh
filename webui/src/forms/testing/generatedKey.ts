// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const PEM_LINE_WIDTH = 64;

let cachedPem: Promise<string> | undefined;

async function generatePem(): Promise<string> {
    const pair = await crypto.subtle.generateKey({ name: "RSASSA-PKCS1-v1_5", modulusLength: 2048, publicExponent: new Uint8Array([1, 0, 1]), hash: "SHA-256" }, true, ["sign", "verify"]);
    const der = new Uint8Array(await crypto.subtle.exportKey("pkcs8", pair.privateKey));
    const base64 = btoa(String.fromCodePoint(...der));
    const lines = Array.from({ length: Math.ceil(base64.length / PEM_LINE_WIDTH) }, (_, i) => base64.slice(i * PEM_LINE_WIDTH, (i + 1) * PEM_LINE_WIDTH));
    return `-----BEGIN PRIVATE KEY-----\n${lines.join("\n")}\n-----END PRIVATE KEY-----\n`;
}

export const generatedPrivateKeyPem = (): Promise<string> => (cachedPem ??= generatePem());

export async function generatedServiceAccountKey(clientEmail = "bot@example-project.iam.gserviceaccount.com"): Promise<string> {
    return JSON.stringify({ type: "service_account", project_id: "example-project", client_email: clientEmail, private_key: await generatedPrivateKeyPem() });
}

const TRUNCATED_LENGTH = 60;
const PEM_BEGIN = "-----BEGIN PRIVATE KEY-----\n";
const PEM_END = "-----END PRIVATE KEY-----\n";

const jsonEscape = (text: string) => JSON.stringify(text).slice(1, -1);

function keyVariants(pem: string): Record<string, string> {
    return {
        "{{PRIVATE_KEY_PEM_TRUNCATED_60}}": pem.slice(0, TRUNCATED_LENGTH),
        "{{PRIVATE_KEY_PEM_NO_ARMOUR}}": pem.slice(PEM_BEGIN.length, pem.length - PEM_END.length).trimEnd(),
        "{{PRIVATE_KEY_PEM_NO_END}}": pem.slice(0, pem.length - PEM_END.length),
        "{{PRIVATE_KEY_PEM_LABEL_MISMATCH}}": pem.replace("-----END PRIVATE KEY-----", "-----END RSA PRIVATE KEY-----"),
        "{{PRIVATE_KEY_PEM}}": pem,
    };
}

export async function withGeneratedKey(input: string): Promise<string> {
    if (!input.includes("{{PRIVATE_KEY_PEM")) return input;
    const pem = await generatedPrivateKeyPem();
    return Object.entries(keyVariants(pem)).reduce((text, [token, value]) => text.replaceAll(token, jsonEscape(value)), input);
}
