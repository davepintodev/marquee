import { tmpdir } from "node:os";
import { join } from "node:path";
import { defineConfig } from "@playwright/test";

// Screenshots, recordings and traces stay out of the repo.
const out = process.env.MARQUEE_SHOTS_DIR ?? join(tmpdir(), "mrq-229-shots");
const listen = process.env.MARQUEE_SMOKE_LISTEN ?? "127.0.0.1:18080";

export default defineConfig({
  testDir: "e2e",
  outputDir: join(out, "playwright"),
  workers: 1,
  reporter: "list",
  use: {
    colorScheme: "dark",
    baseURL: `http://${listen}`,
    video: { mode: "on", size: { width: 1280, height: 720 } },
  },
  metadata: { shots: out },
  webServer: {
    command: "../bin/marquee --fake",
    env: { MARQUEE_LISTEN: listen },
    url: `http://${listen}/api/health`,
    reuseExistingServer: false,
    timeout: 30_000,
  },
});
