import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "VITE_");
  const upstream = env.VITE_PROXY_UPSTREAM || "http://localhost:6674";
  return {
    plugins: [react()],
    server: {
      proxy: {
        "/direct": { target: upstream, changeOrigin: true },
        "/state": { target: upstream, changeOrigin: true },
        "/action": { target: upstream, changeOrigin: true },
      },
    },
  };
});
