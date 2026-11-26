// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import fs from "fs";
import path from "path";
import { fileURLToPath } from "url";
import { defineConfig, loadEnv } from "vite";


const dirname = path.dirname(fileURLToPath(import.meta.url));

function isTruthy(value: string | undefined): boolean {
    return value === "1" || value?.toLowerCase() === "true";
}

function devAuthTokenPlugin(remoteBackendUrl: string | undefined, authToken: string | undefined) {
    const useRemoteBackend = remoteBackendUrl && authToken;
    return {
        name: "dev-auth-token",
        transformIndexHtml() {
            const script = useRemoteBackend
                ? `const token = ${JSON.stringify(authToken)};
                   if (token && localStorage.getItem("access_token") !== token) {
                       localStorage.setItem("access_token", token);
                   }`
                : `if (localStorage.getItem("access_token")) {
                       localStorage.removeItem("access_token");
                   }`;
            return [{ tag: "script", attrs: { type: "module" }, children: script, injectTo: "head-prepend" as const }];
        },
    };
}

function generateVersionMetadata() {
    return {
        name: "generate-version-metadata",
        closeBundle() {
            const outputPath = path.resolve(dirname, "static", "ui-version.json");
            const versionMetadata = {
                id: "solace-agent-mesh-webui",
                name: "Solace Agent Mesh UI",
                description: "Solace Agent Mesh UI",
                version: "sam",
            };
            const outputDir = path.dirname(outputPath);
            if (!fs.existsSync(outputDir)) fs.mkdirSync(outputDir, { recursive: true });
            fs.writeFileSync(outputPath, JSON.stringify(versionMetadata, null, 2) + "\n");
        },
    };
}

export default defineConfig(({ mode }) => {
    const webuiEnv = loadEnv(mode, dirname, "");
    const rootEnv = loadEnv(mode, path.resolve(dirname, ".."), "");
    const env = { ...rootEnv, ...webuiEnv };

    const remoteBackendUrl = env.VITE_BACKEND_URL;
    const authToken = env.VITE_AUTH_TOKEN;

    const productAnalyticsEnabled = mode === "development" || isTruthy(env.SAM_BUILD_PRODUCT_ANALYTICS);

    const backendPort = webuiEnv.VITE_BACKEND_PORT || rootEnv.FASTAPI_PORT || process.env.FASTAPI_PORT || "8800";
    const backendTarget = remoteBackendUrl || `http://localhost:${backendPort}`;

    const createProxyConfig = (target: string) => ({
        target,
        changeOrigin: true,
        secure: false,
        ...(authToken && {
            configure: (proxy: any) => {
                proxy.on("proxyReq", (proxyReq: any) => {
                    proxyReq.setHeader("Authorization", `Bearer ${authToken}`);
                });
            },
        }),
    });

    return {
        root: dirname,
        optimizeDeps: { exclude: ["@silurus/ooxml"] },
        define: {
            ...(mode === "development" ? { __SAM_BACKEND_PORT__: JSON.stringify(backendPort) } : {}),
            __SAM_PRODUCT_ANALYTICS__: JSON.stringify(productAnalyticsEnabled),
        },
        plugins: [react(), tailwindcss(), generateVersionMetadata(), ...(mode === "development" ? [devAuthTokenPlugin(remoteBackendUrl, authToken)] : [])],
        resolve: {
            alias: [
                { find: "@/assets", replacement: path.resolve(dirname, "src/assets") },
                { find: "@", replacement: path.resolve(dirname, "src") },
            ],
        },
        build: {
            outDir: "static",
            emptyOutDir: true,
            rolldownOptions: {
                input: {
                    main: path.resolve(dirname, "index.html"),
                    authCallback: path.resolve(dirname, "auth-callback.html"),
                },
                output: {
                    codeSplitting: {
                        groups: [
                            {
                                name: "vendor",
                                test: /node_modules[\\/](react|react-dom|scheduler|json-edit-react|marked|@tanstack[\\/]react-table|@tanstack[\\/]table-core|lucide-react|html-react-parser)[\\/]/,
                            },
                        ],
                    },
                },
            },
        },
        server: {
            port: 3000,
            host: true,
            proxy: {
                "/api": createProxyConfig(backendTarget),
                "/gw": createProxyConfig(backendTarget),
            },
        },
    };
});
