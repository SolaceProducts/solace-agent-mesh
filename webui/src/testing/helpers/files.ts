// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const createMockFile = (name: string, size: number, type: string): File => {
    const blob = new Blob(["a".repeat(size)], { type });
    return new File([blob], name, { type });
};

export const createMockFileList = (files: File[]): FileList => {
    const fileList: Record<number, File> & {
        length: number;
        item: (index: number) => File | null;
        [Symbol.iterator]: () => IterableIterator<File>;
    } = {
        length: files.length,
        item: (index: number) => files[index] || null,
        [Symbol.iterator]: function* () {
            for (const f of files) {
                yield f;
            }
        },
    };
    files.forEach((file, index) => {
        fileList[index] = file;
    });
    return fileList as FileList;
};
