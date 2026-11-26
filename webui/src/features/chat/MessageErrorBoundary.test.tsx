// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { MessageErrorBoundary } from "./MessageErrorBoundary";

const Boom = (): never => {
    throw new Error("render boom");
};

describe("MessageErrorBoundary", () => {
    afterEach(() => vi.restoreAllMocks());

    it("renders children when they don't throw", () => {
        render(
            <MessageErrorBoundary>
                <div>hello message</div>
            </MessageErrorBoundary>
        );
        expect(screen.getByText("hello message")).toBeInTheDocument();
    });

    it("contains a child render error and shows the inline fallback instead of propagating", () => {
        const errSpy = vi.spyOn(console, "error").mockImplementation(() => {});
        expect(() =>
            render(
                <MessageErrorBoundary>
                    <Boom />
                </MessageErrorBoundary>
            )
        ).not.toThrow();
        expect(screen.getByTestId("message-render-error")).toBeInTheDocument();
        expect(screen.getByText("This message couldn't be displayed.")).toBeInTheDocument();
        expect(errSpy).toHaveBeenCalled();
    });

    it("isolates a throwing message so sibling messages still render", () => {
        vi.spyOn(console, "error").mockImplementation(() => {});
        render(
            <>
                <MessageErrorBoundary>
                    <Boom />
                </MessageErrorBoundary>
                <MessageErrorBoundary>
                    <div>healthy sibling</div>
                </MessageErrorBoundary>
            </>
        );
        expect(screen.getByTestId("message-render-error")).toBeInTheDocument();
        expect(screen.getByText("healthy sibling")).toBeInTheDocument();
    });
});
