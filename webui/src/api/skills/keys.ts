// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const skillKeys = {
    all: ["skills"] as const,
    lists: () => [...skillKeys.all, "list"] as const,
    list: () => [...skillKeys.lists()] as const,
    builtins: () => [...skillKeys.lists(), "builtin"] as const,
    details: () => [...skillKeys.all, "detail"] as const,
    detail: (id: string) => [...skillKeys.details(), id] as const,
    files: (id: string) => [...skillKeys.detail(id), "files"] as const,
    fileContent: (id: string, path: string) => [...skillKeys.detail(id), "fileContent", path] as const,
};
