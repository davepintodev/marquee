import { tmpdir } from "node:os";
import { join } from "node:path";
import { defineConfig } from "@playwright/test";

// Screenshots, recordings and traces stay out of the repo.
const out = process.env.MARQUEE_SHOTS_DIR ?? join(tmpdir(), "mrq-229-shots");
const listen = process.env.MARQUEE_SMOKE_LISTEN ?? "127.0.0.1:18080";
// The real service with TMDB, AIOStreams, the debrid file server and Jellyfin faked.
const realListen = process.env.MARQUEE_SMOKE_REAL_LISTEN ?? "127.0.0.1:18081";

export default defineConfig({
  testDir: "e2e",
  outputDir: join(out, "playwright"),
  workers: 1,
  reporter: "list",
  use: {
    colorScheme: "dark",
    video: { mode: "on", size: { width: 1280, height: 720 } },
  },
  metadata: { shots: out },
  projects: [
    { name: "fake", testIgnore: "real.spec.ts", use: { baseURL: `http://${listen}` } },
    { name: "real", testMatch: "real.spec.ts", use: { baseURL: `http://${realListen}` } },
  ],
  webServer: [
    {
      command: "../bin/marquee --fake",
      env: { MARQUEE_LISTEN: listen },
      url: `http://${listen}/api/health`,
      reuseExistingServer: false,
      timeout: 30_000,
    },
    {
      command: `../bin/smokeserve --listen ${realListen}`,
      url: `http://${realListen}/`,
      reuseExistingServer: false,
      timeout: 60_000,
    },
  ],
});
