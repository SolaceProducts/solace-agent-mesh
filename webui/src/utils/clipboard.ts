// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export async function copyToClipboard(text: string): Promise<boolean> {
    try {
        await navigator.clipboard.writeText(text);
        return true;
    } catch (err) {
        console.error("Failed to copy to clipboard:", err);
        return false;
    }
}

export function copyDeferredToClipboard(textPromise: Promise<string>): Promise<boolean> {
    try {
        const item = new ClipboardItem({
            "text/plain": textPromise.then(text => new Blob([text], { type: "text/plain" })),
        });
        return navigator.clipboard.write([item]).then(() => true);
    } catch (err) {
        console.error("Failed to copy to clipboard (deferred):", err);
        return Promise.resolve(false);
    }
}
