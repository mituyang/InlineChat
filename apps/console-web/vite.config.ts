import { resolve } from "node:path";

import vue from "@vitejs/plugin-vue";
import { defineConfig } from "vite";

export default defineConfig(({ command, mode }) => {
  if (command === "build") {
    const target = mode === "admin" ? "admin" : "agent";
    const root = resolve(__dirname, target);
    return {
      root,
      plugins: [vue()],
      base: "./",
      build: {
        outDir: resolve(__dirname, `dist/${target}`),
        emptyOutDir: true,
        assetsDir: "assets",
      },
    };
  }

  return {
    plugins: [vue()],
    base: "/",
    server: {
      host: "0.0.0.0",
      port: 5173,
      proxy: {
        "/api": "http://127.0.0.1:8200",
        "/app": "http://127.0.0.1:8200",
        "/docs": "http://127.0.0.1:8200",
        "/sdk": "http://127.0.0.1:8200",
        "/ws": {
          target: "ws://127.0.0.1:8200",
          ws: true,
        },
      },
    },
  };
});
