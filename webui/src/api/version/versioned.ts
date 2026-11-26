// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { VersionInfo } from "./service";
import { getVersionV2 } from "./v2";

export interface VersionApi {
    get: () => Promise<VersionInfo>;
}

export const versionApi: VersionApi = { get: getVersionV2 };
