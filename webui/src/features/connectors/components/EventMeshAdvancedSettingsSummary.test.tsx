// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { newEventMeshOperation } from "../eventMeshUtils";

import { EventMeshAdvancedSettingsSummary } from "./EventMeshAdvancedSettingsSummary";

describe("EventMeshAdvancedSettingsSummary", () => {
    test("renders the field labels and mapped select values for a request-reply operation", () => {
        const operation = { ...newEventMeshOperation(), wait_for_response: true, request_expiry_ms: 45000, reply_mode: "p2p_inbox" as const, qos: 1 as const, payload_format: "json" as const };
        render(<EventMeshAdvancedSettingsSummary operation={operation} />);

        expect(screen.getByText("Advanced Settings")).toBeInTheDocument();
        expect(screen.getByText("Payload Schema")).toBeInTheDocument();
        expect(screen.getByText("Delivery Mode")).toBeInTheDocument();
        expect(screen.getByText("Timeout (ms)")).toBeInTheDocument();
        expect(screen.getByText("Reply Delivery")).toBeInTheDocument();

        expect(screen.getByText("JSON")).toBeInTheDocument();
        expect(screen.getByText("Guaranteed Messaging")).toBeInTheDocument();
        expect(screen.getByText("Direct Reply-To")).toBeInTheDocument();
        expect(screen.getByText("45000")).toBeInTheDocument();
    });

    test("hides timeout and reply-delivery fields when not waiting for a response", () => {
        const operation = { ...newEventMeshOperation(), wait_for_response: false, qos: 0 as const, payload_format: "text" as const };
        render(<EventMeshAdvancedSettingsSummary operation={operation} />);

        expect(screen.queryByText("Timeout (ms)")).toBeNull();
        expect(screen.queryByText("Reply Delivery")).toBeNull();
        expect(screen.getByText("Direct Messaging")).toBeInTheDocument();
        expect(screen.getByText("Plain text")).toBeInTheDocument();
    });
});
