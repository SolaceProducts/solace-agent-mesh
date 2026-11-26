// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { connectionTestDestinationChanged, isConnectionTestCredential } from "./connectionTestDestination";

const savedBroker = { broker_url: "tcps://broker.example.com:55443", broker_password: "<REDACTED>" };
const savedMail = { imap_host: "imap.example.com", imap_port: 993, imap_password: "<REDACTED>" };

describe("connectionTestDestinationChanged", () => {
    test.each([
        ["the form is unchanged", "event_mesh", savedBroker, { broker_url: "tcps://broker.example.com:55443", broker_password: "" }, false],
        ["the saved URL is resent with spaces", "event_mesh", savedBroker, { broker_url: "  tcps://broker.example.com:55443 " }, false],
        ["the URL is blank", "event_mesh", savedBroker, { broker_url: "" }, false],
        ["the URL is the redacted placeholder", "event_mesh", savedBroker, { broker_url: "<REDACTED>" }, false],
        ["the URL is missing from the form", "event_mesh", savedBroker, {}, false],
        ["the host changes", "event_mesh", savedBroker, { broker_url: "tcps://other.example.com:55443" }, true],
        ["only the port changes", "event_mesh", savedBroker, { broker_url: "tcps://broker.example.com:1" }, true],
        ["only the scheme changes", "event_mesh", savedBroker, { broker_url: "tcp://broker.example.com:55443" }, true],
        ["TLS verification is switched off", "event_mesh", savedBroker, { broker_url: savedBroker.broker_url, tls_skip_verify: true }, true],
        ["TLS verification is resent as the saved default", "event_mesh", savedBroker, { tls_skip_verify: false }, false],
        ["TLS verification is resent as text equal to the saved value", "event_mesh", { ...savedBroker, tls_skip_verify: "true" }, { tls_skip_verify: true }, false],
        ["the mail host changes", "email", savedMail, { imap_host: "imap.other.example.com" }, true],
        ["the mail port is resent as text", "email", savedMail, { imap_port: "993" }, false],
        ["the mail port changes", "email", savedMail, { imap_port: 143 }, true],
        ["the default mail port is resent and none is saved", "email", { imap_host: "imap.example.com" }, { imap_port: 993 }, false],
        ["the mail port is blank", "email", savedMail, { imap_port: "" }, false],
        ["the mail port was cleared and reads as NaN", "email", savedMail, { imap_port: Number.NaN }, false],
        ["the saved port is resent as a number", "email", { imap_host: "imap.example.com", imap_port: 143 }, { imap_port: 143 }, false],
        ["the saved port is resent as text, which the probe reads as the default port", "email", { imap_host: "imap.example.com", imap_port: 143 }, { imap_port: "143" }, true],
        ["a saved text port differs from the same number, which the probe reads as the default", "email", { imap_host: "imap.example.com", imap_port: "143" }, { imap_port: 143 }, true],
        ["a saved text port and the default port read the same", "email", { imap_host: "imap.example.com", imap_port: "143" }, { imap_port: 993 }, false],
        ["TLS verification is switched back on", "event_mesh", { ...savedBroker, tls_skip_verify: "true" }, { tls_skip_verify: "false" }, true],
        ["a padded saved TLS value reads as off, so a clean true is a change", "event_mesh", { ...savedBroker, tls_skip_verify: " true" }, { tls_skip_verify: "true" }, true],
        ["a padded form TLS value is trimmed before it is read, as the backend does", "event_mesh", { ...savedBroker, tls_skip_verify: "true" }, { tls_skip_verify: " true " }, false],
        ["a padded saved URL and an untouched form", "event_mesh", { broker_url: " tcps://broker.example.com:55443 " }, { broker_url: "tcps://broker.example.com:55443" }, false],
        ["the host is sent as an object, which no spelling of a host equals", "email", savedMail, { imap_host: { a: "b" } }, true],
        ["the type has no binding", "slack", { bot_token: "<REDACTED>" }, { bot_token: "x" }, false],
    ])("%s", (_name, type, saved, form, want) => {
        expect(connectionTestDestinationChanged(type, saved, form)).toBe(want);
    });

    test("a new entrypoint has nothing saved to compare with", () => {
        expect(connectionTestDestinationChanged("event_mesh", undefined, { broker_url: "tcps://broker.example.com:55443" })).toBe(false);
    });

    test("a form without values has nothing to compare", () => {
        expect(connectionTestDestinationChanged("event_mesh", savedBroker, undefined)).toBe(false);
    });
});

describe("isConnectionTestCredential", () => {
    test.each([
        ["event_mesh", "broker_password", true],
        ["email", "imap_password", true],
        ["email", "oauth2_client_secret", true],
        ["event_mesh", "broker_url", false],
        ["slack", "bot_token", false],
    ])("%s %s", (type, field, want) => {
        expect(isConnectionTestCredential(type, field)).toBe(want);
    });
});
