/// <reference types="vite/client" />

/* 前端可见环境变量。仅 VITE_ 前缀进入产物。
   无前缀变量仅 vite.config.ts 可见，此处禁止声明。 */
interface ImportMetaEnv {
  /** API 基址。空串表示同源。域名禁止写入代码。 */
  readonly VITE_API_BASE?: string
  /** live = 后端真实数据。其余 = mock 数据。 */
  readonly VITE_API_MODE?: string
  /** 运维 ADMIN_TOKEN。仅本机调试经 .env.local 注入。禁止进入线上构建。 */
  readonly VITE_API_TOKEN?: string
  /** true = 编入管理界面。其余值侧栏不显示系统管理。 */
  readonly VITE_ADMIN_PANEL?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
