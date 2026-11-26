// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const DB_VERSION = 1;

interface CacheEntry<T> {
    key: string;
    value: T;
    createdAt: number;
    accessedAt: number;
}

export interface PersistentCacheOptions {
    dbName: string;
    storeName: string;
    maxEntries: number;
    ttlMs?: number;
    memoryMaxEntries?: number;
}

interface PersistentCache<T> {
    getSync(key: string): T | undefined;
    get(key: string): Promise<T | undefined>;
    set(key: string, value: T): Promise<void>;
    delete(key: string): Promise<boolean>;
    clear(): Promise<void>;
}

function openDB(dbName: string, storeName: string): Promise<IDBDatabase | null> {
    return new Promise(resolve => {
        try {
            if (typeof indexedDB === "undefined") {
                resolve(null);
                return;
            }
            const request = indexedDB.open(dbName, DB_VERSION);
            request.onupgradeneeded = () => {
                const db = request.result;
                if (!db.objectStoreNames.contains(storeName)) {
                    const store = db.createObjectStore(storeName, { keyPath: "key" });
                    store.createIndex("accessedAt", "accessedAt", { unique: false });
                }
            };
            request.onsuccess = () => resolve(request.result);
            request.onerror = () => {
                console.warn(`[PersistentCache] Failed to open IndexedDB "${dbName}":`, request.error);
                resolve(null);
            };
        } catch {
            resolve(null);
        }
    });
}

function idbOperation<T>(db: IDBDatabase, storeName: string, mode: IDBTransactionMode, operation: (store: IDBObjectStore) => IDBRequest<T>): Promise<T> {
    return new Promise((resolve, reject) => {
        const tx = db.transaction(storeName, mode);
        const store = tx.objectStore(storeName);
        const request = operation(store);
        request.onsuccess = () => resolve(request.result);
        request.onerror = () => reject(request.error);
    });
}

export function createPersistentCache<T>(options: PersistentCacheOptions): PersistentCache<T> {
    const { dbName, storeName, maxEntries, ttlMs = 0 } = options;
    const memoryMax = Math.min(options.memoryMaxEntries ?? maxEntries, 500);

    const memoryCache = new Map<string, T>();

    let dbPromise: Promise<IDBDatabase | null> | null = null;

    function getDB(): Promise<IDBDatabase | null> {
        if (!dbPromise) {
            dbPromise = openDB(dbName, storeName);
        }
        return dbPromise;
    }

    function isExpired(entry: CacheEntry<T>): boolean {
        if (ttlMs <= 0) return false;
        return Date.now() - entry.createdAt > ttlMs;
    }

    function touchMemory(key: string, value: T): void {
        memoryCache.delete(key);
        memoryCache.set(key, value);
        if (memoryCache.size > memoryMax) {
            const firstKey = memoryCache.keys().next().value;
            if (firstKey !== undefined) memoryCache.delete(firstKey);
        }
    }

    async function evictIfNeeded(db: IDBDatabase): Promise<void> {
        try {
            const count = await idbOperation(db, storeName, "readonly", store => store.count());
            if (count <= maxEntries) return;

            const toEvict = count - maxEntries;
            const tx = db.transaction(storeName, "readwrite");
            const store = tx.objectStore(storeName);
            const index = store.index("accessedAt");
            const request = index.openCursor();
            let evicted = 0;

            await new Promise<void>((resolve, reject) => {
                request.onsuccess = () => {
                    const cursor = request.result;
                    if (cursor && evicted < toEvict) {
                        cursor.delete();
                        evicted++;
                        cursor.continue();
                    } else {
                        resolve();
                    }
                };
                request.onerror = () => reject(request.error);
            });
        } catch (err) {
            console.warn("[PersistentCache] Eviction error:", err);
        }
    }

    return {
        getSync(key: string): T | undefined {
            const value = memoryCache.get(key);
            if (value !== undefined) {
                touchMemory(key, value);
            }
            return value;
        },

        async get(key: string): Promise<T | undefined> {
            const memValue = memoryCache.get(key);
            if (memValue !== undefined) {
                touchMemory(key, memValue);
                return memValue;
            }

            const db = await getDB();
            if (!db) return undefined;

            try {
                const entry = await idbOperation<CacheEntry<T> | undefined>(db, storeName, "readonly", store => store.get(key));

                if (!entry) return undefined;

                if (isExpired(entry)) {
                    idbOperation(db, storeName, "readwrite", store => store.delete(key)).catch(err => console.warn("[PersistentCache] Failed to delete expired entry:", err));
                    return undefined;
                }

                touchMemory(key, entry.value);

                const updated: CacheEntry<T> = { ...entry, accessedAt: Date.now() };
                idbOperation(db, storeName, "readwrite", store => store.put(updated)).catch(err => console.warn("[PersistentCache] Failed to update accessedAt:", err));

                return entry.value;
            } catch (err) {
                console.warn("[PersistentCache] Read error:", err);
                return undefined;
            }
        },

        async set(key: string, value: T): Promise<void> {
            touchMemory(key, value);

            const db = await getDB();
            if (!db) return;

            try {
                const now = Date.now();
                const entry: CacheEntry<T> = {
                    key,
                    value,
                    createdAt: now,
                    accessedAt: now,
                };
                await idbOperation(db, storeName, "readwrite", store => store.put(entry));

                evictIfNeeded(db).catch(err => console.warn("[PersistentCache] Background eviction failed:", err));
            } catch (err) {
                console.warn("[PersistentCache] Write error:", err);
            }
        },

        async delete(key: string): Promise<boolean> {
            const hadMemory = memoryCache.delete(key);

            const db = await getDB();
            if (!db) return hadMemory;

            try {
                await idbOperation(db, storeName, "readwrite", store => store.delete(key));
                return true;
            } catch (err) {
                console.warn("[PersistentCache] Delete error:", err);
                return hadMemory;
            }
        },

        async clear(): Promise<void> {
            memoryCache.clear();

            const db = await getDB();
            if (!db) return;

            try {
                await idbOperation(db, storeName, "readwrite", store => store.clear());
            } catch (err) {
                console.warn("[PersistentCache] Clear error:", err);
            }
        },
    };
}
