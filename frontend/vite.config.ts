import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import solid from "vite-plugin-solid";
import tailwindcss from "@tailwindcss/vite";

// @dto resolves to the Go→TypeScript generated API DTOs
// (internal/apidto/ts/dto.gen.ts, regenerated via `go run ./cmd/gendto`).
// Importing from this single alias keeps request/response shapes generated,
// never hand-duplicated (plan Stage 0 / Guardrail #4-#5). The same alias is
// mirrored in tsconfig.json (paths) and vitest.config.ts (test resolver).
const dtoAlias = fileURLToPath(
  new URL("../internal/apidto/ts/dto.gen.ts", import.meta.url),
);

// The build emits into a subfolder of the Go embed directory
// (internal/web/static/app/), NOT into static/ itself, so it can never
// touch or overwrite the currently-live static/index.html production
// frontend. That atomic cutover happens in a later stage; until then this
// bundle is built and embedded but not yet served as the app shell.
//
// base: "./" keeps asset URLs relative, so the generated index.html works
// regardless of the path it's ultimately mounted at.
export default defineConfig({
  plugins: [solid(), tailwindcss()],
  base: "./",
  resolve: {
    alias: {
      "@dto": dtoAlias,
    },
  },
  // Dev-only: the SolidJS source (pnpm dev) is served by Vite, but every
  // /api/* and /healthz call must hit the Go backend. Point this at a locally
  // running `sakms` (SAKMS_ADDR, default :8080). Overridable via the
  // SAKMS_DEV_BACKEND env var for a non-default port. This block has zero
  // effect on `vite build` / the embedded production bundle.
  server: {
    proxy: {
      "/api": { target: process.env.SAKMS_DEV_BACKEND ?? "http://localhost:8080", changeOrigin: true },
      "/healthz": { target: process.env.SAKMS_DEV_BACKEND ?? "http://localhost:8080", changeOrigin: true },
    },
  },
  build: {
    outDir: "../internal/web/static/app",
    // outDir lives outside the Vite project root, so emptying it is opt-in.
    // Scoped to the app/ subfolder only — static/index.html is never in range.
    emptyOutDir: true,
  },
});
