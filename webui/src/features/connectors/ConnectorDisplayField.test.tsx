// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { ConnectorDisplayField } from "./ConnectorDisplayField";

const openExternalLink = vi.fn();
const isWailsRuntime = vi.fn(() => false);
vi.mock("@/utils/openExternalLink", () => ({
    openExternalLink: (url: string) => openExternalLink(url),
    isWailsRuntime: () => isWailsRuntime(),
}));

const downloadAuthenticatedFile = vi.fn();
vi.mock("@/utils/download", () => ({ downloadAuthenticatedFile: (url: string, filename: string) => downloadAuthenticatedFile(url, filename) }));

describe("ConnectorDisplayField URL rendering", () => {
    beforeEach(() => {
        openExternalLink.mockClear();
        downloadAuthenticatedFile.mockClear();
        isWailsRuntime.mockReturnValue(false);
    });

    test("renders an http value as a link", () => {
        render(<ConnectorDisplayField label="Spec" value="https://platform.example.com/api/v1/platform/connectors/c1/resource" />);
        const link = screen.getByRole("link", { name: /resource/ });
        expect(link).toHaveAttribute("href", "https://platform.example.com/api/v1/platform/connectors/c1/resource");
        expect(link).toHaveAttribute("target", "_blank");
        expect(link).toHaveAttribute("rel", "noopener noreferrer");
    });

    test.each(["http://localhost:8001/api/v1/platform/connectors/c1/resource", "http://localhost:8001/api/v2/platform/connectors/c1/resource"])("downloads the spec resource URL %s via authenticated fetch when not in Wails", async specUrl => {
        render(<ConnectorDisplayField label="API Spec" value={specUrl} />);
        await userEvent.click(screen.getByRole("link", { name: /resource/ }));
        expect(downloadAuthenticatedFile).toHaveBeenCalledWith(specUrl, "api-spec-spec.yaml");
        expect(openExternalLink).not.toHaveBeenCalled();
    });

    test("hands the spec resource URL to openExternalLink when in Wails", async () => {
        isWailsRuntime.mockReturnValue(true);
        render(<ConnectorDisplayField label="API Spec" value="http://localhost:8001/api/v1/platform/connectors/c1/resource" />);
        await userEvent.click(screen.getByRole("link", { name: /resource/ }));
        expect(openExternalLink).toHaveBeenCalledWith("http://localhost:8001/api/v1/platform/connectors/c1/resource");
        expect(downloadAuthenticatedFile).not.toHaveBeenCalled();
    });

    test("routes a non-spec http URL through openExternalLink", async () => {
        render(<ConnectorDisplayField label="Docs" value="https://docs.example.com/openapi" />);
        await userEvent.click(screen.getByRole("link", { name: /openapi/ }));
        expect(openExternalLink).toHaveBeenCalledWith("https://docs.example.com/openapi");
        expect(downloadAuthenticatedFile).not.toHaveBeenCalled();
    });

    test("renders a non-url value as plain text", () => {
        render(<ConnectorDisplayField label="Base" value="not a url" />);
        expect(screen.queryByRole("link")).toBeNull();
        expect(screen.getByText("not a url")).toBeInTheDocument();
    });

    test("does not link non-http schemes", () => {
        render(<ConnectorDisplayField label="Spec" value="objstore://default/connectors/c1/spec.yaml" />);
        expect(screen.queryByRole("link")).toBeNull();
    });

    test("renders an unset value without a link", () => {
        render(<ConnectorDisplayField label="Spec" value={undefined} />);
        expect(screen.queryByRole("link")).toBeNull();
        expect(screen.getByText("—")).toBeInTheDocument();
    });
});
