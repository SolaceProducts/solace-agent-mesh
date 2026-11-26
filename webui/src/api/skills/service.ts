// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import yaml from "js-yaml";
import JSZip from "jszip";

import type { Skill } from "@/types";

import type { SkillsApi } from "./versioned";

export interface SkillConfigReference {
    name: string;
    content?: string;
    artifact?: string;
}

export interface SkillConfigAsset {
    name: string;
    artifact: string;
}

export interface SkillBundleConfig {
    name: string;
    description: string;
    instruction_content?: string;
    tags?: string[];
    examples?: string[];
    required_scopes?: string[];
    required_builtins?: string[];
    tools?: Record<string, unknown>[];
    references?: SkillConfigReference[];
    assets?: SkillConfigAsset[];
}

export type FetchSkillArtifact = (artifactName: string) => Promise<{ content: string; mimeType: string }>;

export interface CreateSkillFromConfigRequest {
    config: SkillBundleConfig;
    fetchArtifact?: FetchSkillArtifact;
    confirmReplace?: (existing: Pick<Skill, "id" | "name">) => Promise<void>;
    deferReplace?: boolean;
}

const FRONTMATTER_KEYS = ["tags", "examples", "required_scopes", "required_builtins", "tools"] as const;

export function buildSkillMd(config: SkillBundleConfig): string {
    const frontmatter: Record<string, unknown> = { name: config.name, description: config.description };
    for (const key of FRONTMATTER_KEYS) {
        const value = config[key];
        if (Array.isArray(value) && value.length > 0) {
            frontmatter[key] = value;
        }
    }
    const header = `---\n${yaml.dump(frontmatter)}---\n`;
    const body = config.instruction_content?.trim();
    return body ? `${header}\n${body}\n` : header;
}

function assertSafeBundleFilename(name: string, kind: "reference" | "asset"): void {
    if (!name || name.includes("/") || name.includes("\\") || name.includes("..")) {
        throw new Error(`Skill ${kind} has an invalid filename "${name}" — use a plain filename without path separators.`);
    }
}

async function fetchArtifactForBundle(config: SkillBundleConfig, artifactName: string, fetchArtifact: FetchSkillArtifact | undefined): Promise<string> {
    if (!fetchArtifact) {
        throw new Error(`Skill "${config.name}" references session artifact "${artifactName}" but no session is available to resolve it.`);
    }
    try {
        const { content } = await fetchArtifact(artifactName);
        return content;
    } catch (err) {
        const detail = err instanceof Error ? err.message : String(err);
        throw new Error(`Skill "${config.name}" references artifact "${artifactName}" which could not be loaded from the session: ${detail}`, { cause: err });
    }
}

const BUNDLE_ENTRY_DATE = new Date(Date.UTC(2000, 0, 1));
const BUNDLE_ENTRY_OPTIONS = { date: BUNDLE_ENTRY_DATE, createFolders: false } as const;

async function buildSkillBundle(payload: CreateSkillFromConfigRequest): Promise<File> {
    const { config, fetchArtifact } = payload;
    const zip = new JSZip();
    zip.file("SKILL.md", buildSkillMd(config), BUNDLE_ENTRY_OPTIONS);

    for (const ref of config.references ?? []) {
        assertSafeBundleFilename(ref.name, "reference");
        if (typeof ref.content === "string" && ref.artifact) {
            throw new Error(`Skill "${config.name}" reference "${ref.name}" has both inline content and an artifact — use exactly one.`);
        }
        if (typeof ref.content === "string") {
            zip.file(`references/${ref.name}`, ref.content, BUNDLE_ENTRY_OPTIONS);
        } else if (ref.artifact) {
            zip.file(`references/${ref.name}`, await fetchArtifactForBundle(config, ref.artifact, fetchArtifact), { ...BUNDLE_ENTRY_OPTIONS, base64: true });
        } else {
            throw new Error(`Skill "${config.name}" reference "${ref.name}" has neither inline content nor an artifact.`);
        }
    }

    for (const asset of config.assets ?? []) {
        assertSafeBundleFilename(asset.name, "asset");
        if (!asset.artifact) {
            throw new Error(`Skill "${config.name}" asset "${asset.name}" is missing its artifact reference.`);
        }
        zip.file(`assets/${asset.name}`, await fetchArtifactForBundle(config, asset.artifact, fetchArtifact), { ...BUNDLE_ENTRY_OPTIONS, base64: true });
    }

    const blob = await zip.generateAsync({ type: "blob" });
    return new File([blob], `${config.name}.zip`, { type: "application/zip" });
}

export async function bundleHash(file: Blob): Promise<string | undefined> {
    if (!globalThis.crypto?.subtle) return undefined;
    const digest = await globalThis.crypto.subtle.digest("SHA-256", await file.arrayBuffer());
    return Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, "0")).join("");
}

export type SkillSaveAction = "created" | "updated" | "unchanged";

export type SavedSkill = { id: string; name: string; action: SkillSaveAction } | { id: string; name: string; action: "replace_deferred"; commit: () => Promise<void> };

function withSkillContext(err: unknown, what: string): Error {
    const cause = err as { message?: string; status?: number; code?: string; errorId?: string } | null;
    return Object.assign(new Error(`${what}: ${cause?.message ?? String(err)}`, { cause: err }), { status: cause?.status, code: cause?.code, errorId: cause?.errorId });
}

async function answered<T>(call: () => Promise<T>): Promise<T> {
    try {
        return await call();
    } catch (err) {
        const cause = err as { status?: number; code?: string } | null;
        if (cause?.status !== undefined || cause?.code !== undefined) throw err;
        throw Object.assign(new Error(err instanceof Error ? err.message : String(err), { cause: err }), { code: "ERR_NETWORK" });
    }
}

async function findExistingSkill(skills: SkillsApi, name: string): Promise<{ existing?: Skill; unreadable?: true }> {
    try {
        return { existing: (await answered(skills.listCustom)).find(skill => skill.name === name) };
    } catch (err) {
        if ((err as { status?: number } | null)?.status === 403) return { unreadable: true };
        throw withSkillContext(err, `Could not check whether skill "${name}" already exists`);
    }
}

function conflictWithoutLookup(err: unknown, name: string): Error {
    return Object.assign(withSkillContext(err, `Skill "${name}" already exists, and this role may not read skills to compare it`), { status: 403 });
}

async function createWithoutLookup(skills: SkillsApi, description: string, file: File, name: string): Promise<Skill> {
    try {
        return await answered(() => skills.create(description, file));
    } catch (err) {
        if ((err as { status?: number } | null)?.status !== 409) throw err;
        throw conflictWithoutLookup(err, name);
    }
}

async function createUnlessItAppeared(skills: SkillsApi, description: string, file: File, name: string): Promise<{ created: Skill } | { existing: Skill }> {
    try {
        return { created: await answered(() => skills.create(description, file)) };
    } catch (err) {
        if ((err as { status?: number } | null)?.status !== 409) throw err;
        const { existing, unreadable } = await findExistingSkill(skills, name);
        if (existing) return { existing };
        throw unreadable ? conflictWithoutLookup(err, name) : err;
    }
}

async function replaceSkillBundle(skills: SkillsApi, existing: Pick<Skill, "id" | "name">, file: File): Promise<void> {
    try {
        await answered(() => skills.uploadZip(existing.id, file));
    } catch (err) {
        throw withSkillContext(err, `Could not update skill "${existing.name}"`);
    }
}

async function existingOrCreated(skills: SkillsApi, looked: { existing?: Skill; unreadable?: true }, description: string, file: File, name: string): Promise<{ created: Skill } | { existing: Skill }> {
    if (looked.existing) return { existing: looked.existing };
    if (looked.unreadable) return { created: await createWithoutLookup(skills, description, file, name) };
    return createUnlessItAppeared(skills, description, file, name);
}

export async function saveSkillFromConfig(skills: SkillsApi, payload: CreateSkillFromConfigRequest): Promise<SavedSkill> {
    const config = { ...payload.config, name: payload.config.name.trim() };
    const file = await buildSkillBundle({ ...payload, config });
    const looked = await findExistingSkill(skills, config.name);
    const found = await existingOrCreated(skills, looked, config.description, file, config.name);
    if ("created" in found) return { id: found.created.id, name: found.created.name, action: "created" };
    const { existing } = found;
    const stored = { id: existing.id, name: existing.name };
    const holdsThisBundle = existing.contentHash && existing.discoveryStatus !== "failed" && existing.contentHash === (await bundleHash(file));
    if (holdsThisBundle) return { ...stored, action: "unchanged" };
    if (payload.deferReplace) return { ...stored, action: "replace_deferred", commit: () => replaceSkillBundle(skills, stored, file) };
    if (!payload.confirmReplace) {
        throw Object.assign(new Error(`A skill named "${config.name}" already exists on the platform with different contents.`), { status: 409 });
    }
    await payload.confirmReplace(stored);
    await replaceSkillBundle(skills, stored, file);
    return { ...stored, action: "updated" };
}

export function isTextContentType(contentType: string): boolean {
    const ct = contentType.toLowerCase();
    if (ct.startsWith("text/")) return true;
    return ["json", "markdown", "yaml", "xml"].some(token => ct.includes(token));
}

export interface SkillFileContent {
    contentType: string;
    text?: string;
    blob?: Blob;
}

export async function skillFileContentFrom(response: Response): Promise<SkillFileContent> {
    const contentType = response.headers.get("content-type") ?? "";
    if (isTextContentType(contentType)) {
        return { contentType, text: await response.text() };
    }
    if (contentType.startsWith("image/")) {
        return { contentType, blob: await response.blob() };
    }
    return { contentType };
}
