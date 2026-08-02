import { defineConfig, loadEnv } from 'vite'
import react from '@vitejs/plugin-react'

/* 环境由 vite mode 区分：development / test / production。
   取值见 .env 与 .env.[mode]；本机覆盖写 .env.local。
   VITE_API_BASE 默认空串，表示同源。域名由反向代理决定。
   开发期 proxy 转发到 DEV_API_PROXY（默认 127.0.0.1:8080）。 */
export default defineConfig(({ mode }) => {
  // 第三参数 '' 同时读取无 VITE_ 前缀的变量。此类变量仅本文件使用。
  // 不会进入浏览器产物，可存放内网地址。
  const env = loadEnv(mode, process.cwd(), '')
  const target = env.DEV_API_PROXY || 'http://127.0.0.1:8080'
  return {
    plugins: [react()],
    server: {
      port: 5173,
      proxy: {
        '/api': { target, changeOrigin: true },
        '/health': { target, changeOrigin: true },
      },
    },
  }
})
