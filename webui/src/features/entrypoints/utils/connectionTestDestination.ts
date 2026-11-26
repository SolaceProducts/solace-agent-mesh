// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const REDACTED = "<REDACTED>";
const DEFAULT_IMAP_PORT = 993;

type Values = Record<string, unknown>;

interface DestinationField {
    key: string;
    read: (values: Values) => string;
}

interface ConnectionTestBinding {
    destination: DestinationField[];
    credentials: string[];
}

function suppliedText(value: unknown): string | null {
    if (typeof value === "string") {
        const text = value.trim();
        return text === "" || text === REDACTED ? null : text;
    }
    if (typeof value === "number") return Number.isFinite(value) ? String(value) : null;
    if (typeof value === "boolean") return String(value);
    if (typeof value === "object" && value !== null) return JSON.stringify(value);
    return null;
}

const textField = (key: string): DestinationField => ({ key, read: values => suppliedText(values[key]) ?? "" });

const imapPortField: DestinationField = {
    key: "imap_port",
    read: values => {
        const port = values.imap_port;
        return typeof port === "number" && Number.isFinite(port) ? String(Math.trunc(port)) : String(DEFAULT_IMAP_PORT);
    },
};

const tlsSkipVerifyField: DestinationField = {
    key: "tls_skip_verify",
    read: values => String(values.tls_skip_verify === true || values.tls_skip_verify === "true"),
};

const CONNECTION_TEST_BINDINGS: Record<string, ConnectionTestBinding> = {
    event_mesh: {
        destination: [textField("broker_url"), tlsSkipVerifyField],
        credentials: ["broker_password"],
    },
    email: {
        destination: [textField("imap_host"), imapPortField],
        credentials: ["imap_password", "oauth2_client_secret"],
    },
};

export function connectionTestDestinationChanged(entrypointType: string, savedValues: Values | undefined, formValues: Values | undefined): boolean {
    const binding = CONNECTION_TEST_BINDINGS[entrypointType];
    if (!binding || !savedValues || !formValues) return false;
    const submitted = withTrimmedDestination(formValues, binding.destination);
    return binding.destination.some(field => suppliedText(submitted[field.key]) !== null && field.read(submitted) !== field.read(savedValues));
}

function withTrimmedDestination(formValues: Values, fields: DestinationField[]): Values {
    const trimmed = { ...formValues };
    for (const { key } of fields) {
        const value = trimmed[key];
        if (typeof value === "string") trimmed[key] = value.trim();
    }
    return trimmed;
}

export function isConnectionTestCredential(entrypointType: string, fieldName: string): boolean {
    return CONNECTION_TEST_BINDINGS[entrypointType]?.credentials.includes(fieldName) ?? false;
}
