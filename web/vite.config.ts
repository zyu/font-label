import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5180,
    proxy: {
      "/api": {
        target: "http://127.0.0.1:18090",
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: "dist",
    sourcemap: false,
    rollupOptions: {
      output: {
        // 体积大、更新少的依赖单独成块，便于浏览器长期缓存
        manualChunks: {
          antd: ["antd", "@ant-design/icons"],
          charts: ["recharts"],
          react: ["react", "react-dom", "react-router-dom"],
        },
      },
    },
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
  },
});
