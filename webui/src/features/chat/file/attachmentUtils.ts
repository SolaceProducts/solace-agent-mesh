// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { nextUniqueFilename } from "../paste/pasteUtils";

export const MAX_THUMBNAIL_FILE_BYTES = 20 * 1024 * 1024;

const IMAGE_EXTENSIONS = ["jpg", "jpeg", "png", "gif", "webp", "bmp", "svg", "ico"];

const TEXT_EXTENSIONS = ["txt", "md", "markdown", "json", "yaml", "yml", "xml", "html", "htm", "css", "scss", "js", "jsx", "ts", "tsx", "py", "java", "c", "cpp", "h", "sh", "log", "csv", "tsv", "ini", "toml"];

const TEXT_MIME_KEYWORDS = ["json", "xml", "javascript", "typescript", "markdown", "yaml", "yml"];

function getFileExtension(filename: string): string | undefined {
    return filename.toLowerCase().split(".").pop();
}

export function isImageType(mimeType: string, filename?: string): boolean {
    if (mimeType.startsWith("image/")) return true;
    if (!filename) return false;
    const ext = getFileExtension(filename);
    return !!ext && IMAGE_EXTENSIONS.includes(ext);
}

export function supportsTextPreview(mimeType: string, filename?: string): boolean {
    if (mimeType.startsWith("text/")) return true;
    if (TEXT_MIME_KEYWORDS.some(k => mimeType.includes(k))) return true;
    if (!filename) return false;
    const ext = getFileExtension(filename);
    return !!ext && TEXT_EXTENSIONS.includes(ext);
}

const originalNames = new WeakMap<File, string>();

function originalName(file: File): string {
    return originalNames.get(file) ?? file.name;
}

function withName(file: File, name: string): File {
    if (name === file.name) return file;
    const renamed = new File([file], name, { type: file.type, lastModified: file.lastModified });
    originalNames.set(renamed, originalName(file));
    return renamed;
}

function isSameFile(a: File, b: File): boolean {
    return originalName(a) === originalName(b) && a.size === b.size && a.lastModified === b.lastModified;
}

export function mergeAttachments(existing: File[], incoming: File[], reservedNames: Iterable<string>): File[] {
    const added = incoming.filter(file => !existing.some(current => isSameFile(current, file)));
    if (added.length === 0) return existing;

    const taken = new Set(reservedNames);
    for (const file of existing) taken.add(file.name);

    return [
        ...existing,
        ...added.map(file => {
            const name = nextUniqueFilename(file.name, taken);
            taken.add(name);
            return withName(file, name);
        }),
    ];
}

export function getExtensionLabel(filename: string): string {
    const parts = filename.split(".");
    const ext = parts.length > 1 ? parts[parts.length - 1].toUpperCase() : "FILE";
    return ext.length > 4 ? ext.substring(0, 4) : ext;
}
