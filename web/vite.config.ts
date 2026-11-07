import { randomUUID } from "node:crypto";
import path from "node:path";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { foldkit } from "@foldkit/vite-plugin";
import { defineConfig } from "vite";

// The app compares its build with the build in /ui-version of the server.
// A difference means that the server has a new UI.
const build = randomUUID();

export default defineConfig({
  define: {
    UI_BUILD: JSON.stringify(build),
    UI_RELEASE: JSON.stringify(process.env.RELEASE ?? ""),
  },
  plugins: [
    ...foldkit({ devToolsMcpPort: false }),
    react(),
    tailwindcss(),
    {
      name: "ui-version",
      generateBundle() {
        this.emitFile({ type: "asset", fileName: "ui-version", source: build });
      },
    },
  ],
  build: {
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (
            id.includes("/node_modules/foldkit/") ||
            id.includes("/node_modules/@foldkit/") ||
            id.includes("/node_modules/effect/") ||
            id.includes("/node_modules/@effect/")
          ) {
            return "foldkit-effect";
          }
        },
      },
    },
  },
  resolve: {
    alias: {
      "@": path.resolve(import.meta.dirname, "./src"),
    },
  },
  server: {
    proxy: {
      "/api": "http://127.0.0.1:6363",
    },
  },
});
