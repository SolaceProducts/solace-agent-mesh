// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { GatewayFieldSchema } from "@/types";

export type FieldEntry = [string, GatewayFieldSchema];

export const DEFAULT_SECTION_TITLE = "Entrypoint Configuration";

export interface FieldSection {
    title: string;
    fields: FieldEntry[];
}

export const groupFieldsIntoSections = (fields: FieldEntry[]): FieldSection[] => {
    const sections: FieldSection[] = [{ title: DEFAULT_SECTION_TITLE, fields: [] }];
    for (const entry of fields) {
        const title = entry[1].section ?? DEFAULT_SECTION_TITLE;
        const section = sections.find(candidate => candidate.title === title);
        if (section) {
            section.fields.push(entry);
        } else {
            sections.push({ title, fields: [entry] });
        }
    }
    return sections;
};
