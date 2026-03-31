#!/usr/bin/env node
// esbuild wrapper: 只 external 必須與宿主共享的模組，其餘第三方庫打包進 bundle

import { build } from 'esbuild';

const [entry, outfile] = process.argv.slice(2);
if (!entry || !outfile) {
    console.error('Usage: node esbuild-plugin-bundle.mjs <entry> <outfile>');
    process.exit(1);
}

// 必須與宿主共享實例的模組（React state/hooks、store 狀態）
const SHARED = new Set([
    'react',
    'react/jsx-runtime',
    'react/jsx-dev-runtime',
    'react-dom',
    'react-dom/client',
    'zustand',
    'zustand/middleware',
    'i18next',
    'react-i18next',
    '@cubelv/sdk',
]);

const externalSharedOnly = {
    name: 'external-shared-only',
    setup(b) {
        b.onResolve({ filter: /^[^./]/ }, (args) => {
            if (SHARED.has(args.path)) {
                return { path: args.path, external: true };
            }
            return undefined;
        });
    },
};

try {
    await build({
        entryPoints: [entry],
        bundle: true,
        format: 'iife',
        globalName: '__plugin__',
        jsx: 'automatic',
        loader: { '.tsx': 'tsx', '.ts': 'ts', '.css': 'css' },
        outfile,
        plugins: [externalSharedOnly],
    });
} catch (err) {
    console.error(err.message);
    process.exit(1);
}
