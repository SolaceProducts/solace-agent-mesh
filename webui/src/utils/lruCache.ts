// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export function createLruCache<T>(maxSize: number) {
    const cache = new Map<string, T>();

    return {
        get(key: string): T | undefined {
            const value = cache.get(key);
            if (value !== undefined) {
                cache.delete(key);
                cache.set(key, value);
            }
            return value;
        },

        set(key: string, value: T): void {
            if (cache.has(key)) {
                cache.delete(key);
            } else if (cache.size >= maxSize) {
                const firstKey = cache.keys().next().value;
                if (firstKey !== undefined) {
                    cache.delete(firstKey);
                }
            }
            cache.set(key, value);
        },

        has(key: string): boolean {
            return cache.has(key);
        },

        delete(key: string): boolean {
            return cache.delete(key);
        },

        clear(): void {
            cache.clear();
        },

        get size(): number {
            return cache.size;
        },
    };
}
