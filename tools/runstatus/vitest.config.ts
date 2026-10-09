import { defineConfig } from "vitest/config";
import vue from "@vitejs/plugin-vue";

export default defineConfig({
  plugins: [vue()],
  test: {
    environment: "happy-dom",
    include: ["tests/unit/**/*.test.ts"],
    setupFiles: ["tests/unit/setup.ts"],
    globals: false,
    reporters: ["verbose"],
    pool: "forks",
    poolOptions: {
      forks: {
        singleFork: false,
      },
    },
  },
});
