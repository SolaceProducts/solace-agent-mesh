// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useState } from "react";

export function useWebStorage<T>(key: string, initialValue: T, storage: Storage) {
    const [storedValue, setStoredValue] = useState<T>(() => {
        if (typeof window === "undefined") {
            return initialValue;
        }
        try {
            const item = storage.getItem(key);
            if (item) {
                return JSON.parse(item);
            } else {
                storage.setItem(key, JSON.stringify(initialValue));
                return initialValue;
            }
        } catch (error) {
            console.warn(`Error reading storage key "${key}":`, error);
            return initialValue;
        }
    });

    const setValue = useCallback(
        (value: T | ((val: T) => T)) => {
            try {
                setStoredValue(prevValue => {
                    const valueToStore = value instanceof Function ? value(prevValue) : value;

                    if (typeof window !== "undefined") {
                        storage.setItem(key, JSON.stringify(valueToStore));
                    }

                    return valueToStore;
                });
            } catch (error) {
                console.warn(`Error setting storage key "${key}":`, error);
            }
        },
        [key, storage]
    );

    return [storedValue, setValue] as const;
}
