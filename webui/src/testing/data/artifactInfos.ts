// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ArtifactInfo } from "@/types";

export const pdfArtifact: ArtifactInfo = {
    filename: "api-documentation.pdf",
    mimeType: "application/pdf",
    size: 524288,
    lastModified: new Date("2024-03-15T10:00:00Z").toISOString(),
    description: "API reference documentation",
};

export const imageArtifact: ArtifactInfo = {
    filename: "architecture-diagram.png",
    mimeType: "image/png",
    size: 204800,
    lastModified: new Date("2024-03-18T14:30:00Z").toISOString(),
    description: "System architecture overview",
};

export const jsonArtifact: ArtifactInfo = {
    filename: "package.json",
    mimeType: "application/json",
    size: 1024,
    lastModified: new Date("2024-03-17T09:45:00Z").toISOString(),
    description: "",
};

export const markdownArtifact: ArtifactInfo = {
    filename: "README.md",
    mimeType: "text/markdown",
    size: 4096,
    lastModified: new Date("2024-03-16T12:20:00Z").toISOString(),
    description: "Project overview and setup instructions",
};

export const artifactWithLongDescription: ArtifactInfo = {
    filename: "design-spec.pdf",
    mimeType: "application/pdf",
    size: 1048576,
    lastModified: new Date("2024-03-20T11:00:00Z").toISOString(),
    description: "API reference documentation for the project endpoints including authentication, data models, and error handling patterns",
};
