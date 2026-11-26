// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const ErrorLabel = ({ message, className }: { message?: string; className?: string }) => {
    return message ? <div className={`text-xs text-(--error-wMain) ${className}`}>{message}</div> : null;
};
