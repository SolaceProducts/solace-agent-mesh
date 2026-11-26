// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";

import { chromium } from "playwright";

/* global console, document, process, URL */

const VIEWPORT = { width: 1920, height: 1200 };
const OUTPUT_WIDTH = 1920;
const BLUR_AT_VIEWPORT_PX = 8;
const JPEG_QUALITY = 72;
const SETTLE_MS = 8_000;
const EMPTIED_LISTS = /\/api\/v1\/(sessions|tasks|projects|share|scheduledTasks|scheduled)/;
const CUT_OFF = /sse|stream|subscribe|productAnalytics/i;
const MODEL_STATUS_V2 = "/api/v2/platform/modelStatus";

const outDir = path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "src", "assets", "activation");

const { values } = parseArgs({
    options: {
        url: { type: "string" },
        "from-light": { type: "string" },
        "from-dark": { type: "string" },
    },
});

async function emptyListsAndBlockWrites(page) {
    await page.route(
        url => url.pathname.startsWith("/api/"),
        async route => {
            const request = route.request();
            if (request.method() !== "GET") return route.fulfill({ status: 204, body: "" });
            if (CUT_OFF.test(request.url())) return route.abort();
            const { pathname } = new URL(request.url());
            if (pathname === MODEL_STATUS_V2) return route.fulfill({ json: { data: { configured: true } } });
            const response = await route.fetch();
            if (!EMPTIED_LISTS.test(request.url()) || !(response.headers()["content-type"] ?? "").includes("json")) {
                return route.fulfill({ response });
            }
            let body = await response.json().catch(() => null);
            if (Array.isArray(body)) body = [];
            else if (body && Array.isArray(body.data)) {
                body.data = [];
                if (body.meta?.pagination) Object.assign(body.meta.pagination, { count: 0, totalPages: 0, nextPage: null });
            }
            return route.fulfill({ response, json: body });
        }
    );
}

async function shoot(browser, url, colorScheme) {
    const page = await browser.newPage({ viewport: VIEWPORT, colorScheme });
    await emptyListsAndBlockWrites(page);
    await page.goto(url, { waitUntil: "domcontentloaded" });
    await page.waitForTimeout(SETTLE_MS);
    const png = await page.screenshot();
    await page.close();
    return png;
}

async function blurAndShrink(browser, png) {
    const height = Math.round((OUTPUT_WIDTH * VIEWPORT.height) / VIEWPORT.width);
    const blur = (BLUR_AT_VIEWPORT_PX * OUTPUT_WIDTH) / VIEWPORT.width;
    const page = await browser.newPage({ viewport: { width: OUTPUT_WIDTH, height } });
    const src = `data:image/png;base64,${png.toString("base64")}`;
    await page.setContent(
        `<body style="margin:0;overflow:hidden">` +
            `<img src="${src}" style="position:fixed;inset:-24px;width:calc(100% + 48px);height:calc(100% + 48px);object-fit:cover;filter:blur(${blur}px)">` +
            `<img src="${src}" style="position:fixed;inset:0;width:100%;height:100%;filter:blur(${blur}px)">` +
            `</body>`
    );
    await page.waitForFunction(() => [...document.querySelectorAll("img")].every(img => img.complete));
    const jpeg = await page.screenshot({ type: "jpeg", quality: JPEG_QUALITY });
    await page.close();
    return jpeg;
}

async function source(browser, theme) {
    const from = values[`from-${theme}`];
    if (from) return readFile(from);
    if (!values.url) throw new Error("pass --url, or --from-light and --from-dark");
    return shoot(browser, values.url, theme);
}

const browser = await chromium.launch();
try {
    await mkdir(outDir, { recursive: true });
    for (const theme of ["light", "dark"]) {
        const out = path.join(outDir, `backdrop-${theme}.jpg`);
        await writeFile(out, await blurAndShrink(browser, await source(browser, theme)));
        console.log(`wrote ${path.relative(process.cwd(), out)}`);
    }
} finally {
    await browser.close();
}
