// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { platformV2 } from "../common";

import type { VersionInfo } from "./service";

interface VersionProductV2 {
    key: string;
    name: string;
    version: string;
}

interface VersionV2 {
    products: VersionProductV2[];
    minClientVersion: string | null;
    apiVersion: string | null;
}

export async function getVersionV2(): Promise<VersionInfo> {
    const { products } = await platformV2.get<VersionV2>("/version");
    return { products: products.map(({ key, name, version }) => ({ id: key, name, version })) };
}
