// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { formatBytes } from "./format";

export const ZIP_ONLY_ERROR = "Only .zip files are supported.";
export const ZIP_INVALID_ERROR = "File is not a valid ZIP archive (extension matches but contents don't).";

export const MAX_UPLOAD_SIZE_BYTES = 100 * 1024 * 1024;

export interface FileSizeValidationResult {
    valid: boolean;
    error?: string;
    oversizedFiles?: Array<{ name: string; size: number }>;
}

export interface FileSizeValidationOptions {
    maxSizeBytes?: number;
    errorPrefix?: string;
    includeFileSizes?: boolean;
    maxFilesToList?: number;
}

export interface ProjectSizeLimitValidationResult {
    valid: boolean;
    error?: string;
    currentSize: number;
    newSize: number;
    totalSize: number;
}

export function validateFileSizes(files: FileList | File[], options: FileSizeValidationOptions = {}): FileSizeValidationResult {
    const { maxSizeBytes, includeFileSizes = true, maxFilesToList = 3 } = options;

    if (!maxSizeBytes) {
        return { valid: true };
    }

    const fileArray = Array.from(files);
    const oversizedFiles: Array<{ name: string; size: number }> = [];

    for (const file of fileArray) {
        if (file.size > maxSizeBytes) {
            oversizedFiles.push({ name: file.name, size: file.size });
        }
    }

    if (oversizedFiles.length === 0) {
        return { valid: true };
    }

    const maxSizeWithUnit = formatBytes(maxSizeBytes, 0);
    let errorMsg: string;

    if (oversizedFiles.length === 1) {
        const file = oversizedFiles[0];
        if (includeFileSizes) {
            const fileSizeWithUnit = formatBytes(file.size);
            errorMsg = `File "${file.name}" (${fileSizeWithUnit}) exceeds the maximum size of ${maxSizeWithUnit}.`;
        } else {
            errorMsg = `File "${file.name}" exceeds the maximum size of ${maxSizeWithUnit}.`;
        }
    } else {
        const fileList = oversizedFiles.slice(0, maxFilesToList);
        const fileNames = includeFileSizes ? fileList.map(f => `${f.name} (${formatBytes(f.size)})`) : fileList.map(f => f.name);

        const remaining = oversizedFiles.length - maxFilesToList;
        const suffix = remaining > 0 ? ` and ${remaining} more` : "";

        errorMsg = `${oversizedFiles.length} files exceed the maximum size of ${maxSizeWithUnit}: ${fileNames.join(", ")}${suffix}`;
    }

    return {
        valid: false,
        error: errorMsg,
        oversizedFiles,
    };
}

export function validateBatchUploadSize(files: FileList | File[], maxBatchUploadSizeBytes?: number): FileSizeValidationResult {
    if (!maxBatchUploadSizeBytes) {
        return { valid: true };
    }

    const totalBatchSize = calculateTotalFileSize(files);

    if (totalBatchSize <= maxBatchUploadSizeBytes) {
        return { valid: true };
    }

    const totalBatchWithUnit = formatBytes(totalBatchSize);
    const limitWithUnit = formatBytes(maxBatchUploadSizeBytes, 0);

    return {
        valid: false,
        error: `Batch upload size (${totalBatchWithUnit}) exceeds limit of ${limitWithUnit}. Please upload fewer files at once.`,
    };
}

export function calculateTotalFileSize(files: FileList | File[] | Array<{ size: number }>): number {
    const fileArray: Array<{ size: number }> = Array.isArray(files) ? files : Array.from(files);
    return fileArray.reduce((sum, file) => sum + file.size, 0);
}

export function isFileSizeValid(file: File, maxSizeBytes?: number): boolean {
    if (!maxSizeBytes) return true;
    return file.size <= maxSizeBytes;
}

export function createFileSizeErrorMessage(filename: string, actualSize: number, maxSize: number): string {
    const actualSizeWithUnit = formatBytes(actualSize);
    const maxSizeWithUnit = formatBytes(maxSize);
    return `File "${filename}" is too large: ${actualSizeWithUnit} exceeds the maximum allowed size of ${maxSizeWithUnit}.`;
}

export function validateProjectSizeLimit(currentProjectSizeBytes: number, newFiles: FileList | File[], maxProjectSizeBytes?: number): ProjectSizeLimitValidationResult {
    const newSize = calculateTotalFileSize(newFiles);
    const totalSize = currentProjectSizeBytes + newSize;

    if (!maxProjectSizeBytes) {
        return { valid: true, currentSize: currentProjectSizeBytes, newSize, totalSize };
    }

    if (totalSize <= maxProjectSizeBytes) {
        return { valid: true, currentSize: currentProjectSizeBytes, newSize, totalSize };
    }

    const currentWithUnit = formatBytes(currentProjectSizeBytes);
    const newWithUnit = formatBytes(newSize);
    const totalWithUnit = formatBytes(totalSize);
    const limitWithUnit = formatBytes(maxProjectSizeBytes, 0);

    return {
        valid: false,
        currentSize: currentProjectSizeBytes,
        newSize,
        totalSize,
        error: `Project size limit exceeded. Current: ${currentWithUnit}, New files: ${newWithUnit}, Total: ${totalWithUnit} exceeds limit of ${limitWithUnit}.`,
    };
}

export async function isZipFile(file: File): Promise<boolean> {
    if (file.size < 4) return false;
    const buf = await readBytesWithFileReaderBecauseJsdomBlobLacksArrayBuffer(file.slice(0, 4));
    const bytes = new Uint8Array(buf);
    return bytes[0] === 0x50 && bytes[1] === 0x4b && bytes[2] === 0x03 && bytes[3] === 0x04;
}

function readBytesWithFileReaderBecauseJsdomBlobLacksArrayBuffer(blob: Blob): Promise<ArrayBuffer> {
    return new Promise<ArrayBuffer>((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(reader.result as ArrayBuffer);
        reader.onerror = () => reject(reader.error);
        reader.readAsArrayBuffer(blob);
    });
}
