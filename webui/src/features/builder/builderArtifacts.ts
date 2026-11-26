// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const BUILD_MANIFEST_ARTIFACT_FILENAME = "build_manifest.yaml";
export const PLATFORM_COMPONENTS_ARTIFACT_FILENAME = "platform_components.json";
export const APPLY_COMPLETE_ARTIFACT_FILENAME = "apply_complete.json";

export const BUILDER_PRIVATE_ARTIFACTS = new Set<string>([BUILD_MANIFEST_ARTIFACT_FILENAME, PLATFORM_COMPONENTS_ARTIFACT_FILENAME, APPLY_COMPLETE_ARTIFACT_FILENAME]);
