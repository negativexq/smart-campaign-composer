import vue from "@vitejs/plugin-vue";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      "/api": "http://localhost:8081",
      "/readyz": "http://localhost:8081",
    },
  },
  test: {
    environment: "jsdom",
    restoreMocks: true,
  },
});
