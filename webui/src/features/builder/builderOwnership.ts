// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const STORAGE_KEY = "sam_builder_written_resources";

const MAX_SESSIONS = 50;

const STAMPED_KINDS: ReadonlySet<string> = new Set(["connector", "skill"]);

export function isStampedKind(kind: string | undefined): boolean {
    return kind !== undefined && STAMPED_KINDS.has(kind);
}

export interface WrittenResource {
    id: string;
    kind: string;
    stamp?: string;
}

export interface WrittenRecord {
    kind?: string;
    stamp?: string;
}

export type OwnsResource = (resource: { id: string; kind: string }) => boolean;

type StoredEntry = string | WrittenResource;
type Store = Record<string, StoredEntry[]>;

const inMemory = new Map<string, Map<string, WrittenRecord>>();

function readStore(): Store {
    try {
        const raw = localStorage.getItem(STORAGE_KEY);
        const parsed: unknown = raw ? JSON.parse(raw) : {};
        return parsed !== null && typeof parsed === "object" && !Array.isArray(parsed) ? (parsed as Store) : {};
    } catch {
        return {};
    }
}

function writeStore(store: Store): void {
    try {
        localStorage.setItem(STORAGE_KEY, JSON.stringify(store));
    } catch {}
}

function isWrittenResource(entry: unknown): entry is WrittenResource {
    return entry !== null && typeof entry === "object" && typeof (entry as WrittenResource).id === "string";
}

function entriesOf(stored: unknown): StoredEntry[] {
    return Array.isArray(stored) ? stored.filter((entry): entry is StoredEntry => typeof entry === "string" || isWrittenResource(entry)) : [];
}

function recordOf(entry: StoredEntry): [string, WrittenRecord] {
    return typeof entry === "string" ? [entry, {}] : [entry.id, { kind: entry.kind, stamp: entry.stamp }];
}

export function writtenRecords(sessionId: string): ReadonlyMap<string, WrittenRecord> {
    const records = new Map<string, WrittenRecord>(entriesOf(readStore()[sessionId]).map(recordOf));
    for (const [id, record] of inMemory.get(sessionId) ?? []) records.set(id, record);
    return records;
}

export function ownsResource(record: WrittenRecord | undefined, kind: string, platformStamp: string | undefined): boolean {
    if (!record) return false;
    if (!isStampedKind(kind)) return true;
    return record.stamp !== undefined && record.stamp === platformStamp;
}

export function rememberWrittenResources(sessionId: string, written: readonly WrittenResource[]): void {
    if (written.length === 0) return;

    const session = inMemory.get(sessionId) ?? new Map<string, WrittenRecord>();
    for (const { id, kind, stamp } of written) session.set(id, { kind, stamp });
    inMemory.set(sessionId, session);

    const store = readStore();
    const merged = new Map<string, StoredEntry>(entriesOf(store[sessionId]).map(entry => [recordOf(entry)[0], entry]));
    for (const resource of written) merged.set(resource.id, resource);
    delete store[sessionId];
    store[sessionId] = [...merged.values()];
    const sessions = Object.keys(store);
    for (const stale of sessions.slice(0, Math.max(0, sessions.length - MAX_SESSIONS))) delete store[stale];
    writeStore(store);
}
