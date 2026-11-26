// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface Person {
    id: string;
    displayName: string;
    workEmail: string;
    jobTitle?: string;
}

export interface Mention {
    id: string;
    name: string;
    startIndex: number;
    endIndex: number;
}

export interface PeopleSearchResponse {
    data: Person[];
}
