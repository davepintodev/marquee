import { defineConfig, configDefaults } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: { proxy: { "/api": "http://localhost:8080" } },
  // e2e/ is the Playwright smoke run, started by `make smoke`.
  test: { exclude: [...configDefaults.exclude, "e2e/**"] },
});
