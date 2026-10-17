import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { federation } from "@module-federation/vite";

// facility-mfe: the facility-layout remote. Exposes ./App -- the shell
// lazy-loads it at /facility/*. Also runnable standalone on :5186 for
// local development without the shell (see main.tsx).
// `base` is the deployment-time asset namespace. In the kind cluster this
// remote is served by its own nginx pod behind the Nginx web gateway at
// http://localhost/mfes/facility-layout/, so every hashed chunk and the
// federation remoteEntry.js must resolve under that prefix -- otherwise a
// dynamically imported chunk would request /assets/... at the shell's origin
// root and collide with every other remote's assets.
//
// Kept as a plain object rather than the ({ command }) => ({...}) callback
// form on purpose: vitest.config.ts does mergeConfig(viteConfig, ...) and Vite
// throws "Cannot merge config in form of callback" on a function export, which
// breaks the whole test suite. Reading the command off process.argv keeps this
// a static object.
const IS_BUILD = process.argv.includes("build");
const PUBLIC_BASE = IS_BUILD ? "/mfes/facility-layout/" : "/";

export default defineConfig({
  base: PUBLIC_BASE,
  plugins: [
    react(),
    federation({
      name: "facility_mfe",
      filename: "remoteEntry.js",
      // No federated type generation. warehouse-console (the only host) never
      // consumes remote types: it declares every remote itself and imports it
      // as `// @ts-expect-error` + lazy(() => import("<name>/App")), with no
      // @mf-types paths in its tsconfig. With the default dts settings the
      // plugin ran tsc against web/tsconfig.json, a solution-style file
      // (`files: []` + references, no compilerOptions, so no `jsx`), and every
      // build printed "Failed to generate type declaration #TYPE-001" for
      // types nobody downloads. If a host ever needs them, re-enable with
      // `dts: { tsConfigPath: "./tsconfig.app.json" }`.
      dts: false,
      exposes: {
        "./App": "./src/App.tsx",
      },
      shared: {
        react: { singleton: true, requiredVersion: "^19.3.0" },
        "react-dom": { singleton: true, requiredVersion: "^19.3.0" },
        "react-router-dom": { singleton: true, requiredVersion: "^7.18.3" },
        "@warehouse/ui-kit": { singleton: true },
      },
    }),
  ],
  server: {
    port: 5186,
    strictPort: true,
    cors: true,
    origin: "http://localhost:5186",
  },
  preview: {
    port: 5186,
    strictPort: true,
    cors: true,
  },
  build: {
    target: "esnext",
    modulePreload: false,
  },
});
