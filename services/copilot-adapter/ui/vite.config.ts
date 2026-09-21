// vitest/config, not vite/config: this file carries the `test` block too.
// It is excluded from tsconfig.json's `include` because vitest 2 bundles its
// own vite 5 types, which do not unify with the vite 6 the app builds with.
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// The Go adapter's documented dev address. The UI runs bare with `npm run dev`
// and proxies the contract's two public prefixes at it.
const adapter = "http://127.0.0.1:8090";

export default defineConfig({
  // The mounting binary serves the bundle at /ui/; hash routing and the
  // same-origin /v1 client are unaffected by the base.
  base: "/ui/",
  plugins: [react()],
  server: {
    // The UI and its API proxy are same-origin. Never grant another browser
    // origin access to the operator's unauthenticated loopback adapter.
    cors: false,
    proxy: {
      "/v1": { target: adapter, changeOrigin: false, ws: true },
      "/healthz": { target: adapter, changeOrigin: false },
    },
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test-setup.ts"],
  },
});
