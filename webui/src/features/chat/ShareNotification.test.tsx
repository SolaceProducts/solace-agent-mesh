// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { ShareNotification } from "@/features/chat/ShareNotification";

const RECENT_TIMESTAMP = Date.now() - 30_000;

describe("ShareNotification", () => {
    test("renders user-specific share message", () => {
        render(<ShareNotification sharedBy="Alice" shareType="user-specific" sharedWith="Bob" sharedAt={RECENT_TIMESTAMP} />);

        expect(screen.getByText("Alice")).toBeInTheDocument();
        expect(screen.getByText("Bob")).toBeInTheDocument();
        expect(screen.getByText(/shared this chat with/)).toBeInTheDocument();
    });

    test("renders domain-restricted share message", () => {
        render(<ShareNotification sharedBy="Alice" shareType="domain-restricted" sharedAt={RECENT_TIMESTAMP} />);

        expect(screen.getByText("Alice")).toBeInTheDocument();
        expect(screen.getByText("your domain")).toBeInTheDocument();
    });

    test("renders authenticated share message", () => {
        render(<ShareNotification sharedBy="Alice" shareType="authenticated" sharedAt={RECENT_TIMESTAMP} />);

        expect(screen.getByText("authenticated users")).toBeInTheDocument();
    });

    test("renders public link share message", () => {
        render(<ShareNotification sharedBy="Alice" shareType="public" sharedAt={RECENT_TIMESTAMP} />);

        expect(screen.getByText("public link")).toBeInTheDocument();
    });

    test("displays relative timestamp", () => {
        render(<ShareNotification sharedBy="Alice" shareType="public" sharedAt={RECENT_TIMESTAMP} />);
        expect(screen.getByText(/\d+s ago/)).toBeInTheDocument();
    });
});
