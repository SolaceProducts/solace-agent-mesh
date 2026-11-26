// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { ProfileSettings } from "./ProfileSettings";

describe("ProfileSettings", () => {
    it("merges User and Email into a single row when they're the same value", () => {
        renderWithProviders(<ProfileSettings />, { authContextValues: { userInfo: { username: "jane@example.com", email: "jane@example.com", roles: [] } } });

        expect(screen.getByText("jane@example.com")).toBeInTheDocument();
        expect(screen.queryByText("Email")).not.toBeInTheDocument();
    });

    it("shows a fallback message when the user holds no roles", () => {
        renderWithProviders(<ProfileSettings />, { authContextValues: { userInfo: { username: "jane.doe", roles: [] } } });

        expect(screen.getByText("No roles assigned")).toBeInTheDocument();
    });
});
