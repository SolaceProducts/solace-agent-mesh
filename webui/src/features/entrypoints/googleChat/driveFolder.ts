// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const DRIVE_FOLDER_ID = /^[A-Za-z0-9_-]{10,128}$/;

const FOLDER_LINKS = [/^(?:https?:\/\/)?drive\.google\.com\/drive\/(?:u\/\d+\/)?(?:mobile\/)?folders\/([^/?#]+)/, /^(?:https?:\/\/)?drive\.google\.com\/open\?(?:.*&)?id=([^&#]+)/];

const FILE_LINK = /^(?:https?:\/\/)?(?:drive\.google\.com\/file\/|docs\.google\.com\/)/;
const DRIVE_LINK = /^(?:https?:\/\/)?drive\.google\.com\//;

export const DRIVE_FOLDER_MESSAGES = {
    fileLink: "That's a link to a file. Paste the link of the folder where the files go.",
    noFolder: "Open the folder itself in Drive and paste its link.",
    shape: "Paste the folder's link from Drive, or its ID.",
} as const;

export function driveFolderIdFromInput(raw: string): string {
    const trimmed = raw.trim();
    for (const link of FOLDER_LINKS) {
        const id = link.exec(trimmed)?.[1];
        if (id) return id;
    }
    return trimmed;
}

export function isValidDriveFolderId(id: string): boolean {
    return DRIVE_FOLDER_ID.test(id);
}

export function driveFolderProblem(raw: string): string | null {
    const id = driveFolderIdFromInput(raw);
    if (id === "" || isValidDriveFolderId(id)) return null;
    if (FILE_LINK.test(id)) return DRIVE_FOLDER_MESSAGES.fileLink;
    if (DRIVE_LINK.test(id)) return DRIVE_FOLDER_MESSAGES.noFolder;
    return DRIVE_FOLDER_MESSAGES.shape;
}
