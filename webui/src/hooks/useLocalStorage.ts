// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useWebStorage } from "./useWebStorage";

export function useLocalStorage<T>(key: string, initialValue: T) {
    return useWebStorage(key, initialValue, window.localStorage);
}
