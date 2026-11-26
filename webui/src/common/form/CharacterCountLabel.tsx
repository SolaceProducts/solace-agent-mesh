// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const CharacterCountLabel = ({ currentLength, maxLength }: { currentLength?: number; maxLength: number }) => {
    return <div className="text-right text-xs text-(--secondary-text-wMain)">{`${currentLength || 0}/${maxLength}`}</div>;
};
