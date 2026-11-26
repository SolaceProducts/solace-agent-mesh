// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export function formatNotificationTime(iso: string): string {
    const date = new Date(iso);
    if (Number.isNaN(date.getTime())) {
        return "";
    }
    const hours = date.getHours();
    const minutes = String(date.getMinutes()).padStart(2, "0");
    return `${date.getDate()}/${date.getMonth() + 1}/${date.getFullYear()}, ${hours % 12 || 12}:${minutes} ${hours < 12 ? "am" : "pm"}`;
}
