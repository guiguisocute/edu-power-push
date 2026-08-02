/* 管理入口可见性判定。
   禁止 import admin/*。运维界面懒加载，普通访客不下载。
   此处仅控制入口是否显示。真正权限在后端现查角色。 */

import { IS_LIVE } from '../api/mode'

export type Role = 'user' | 'operator' | 'admin'

/** 构建期开关。不为 true 时管理界面不编入产物。 */
export const ADMIN_UI_ENABLED = import.meta.env.VITE_ADMIN_PANEL === 'true'

/* 管理视图与用户视图共用同一套 view 与侧栏。 */
export const ADMIN_VIEWS = [
  'sys-console',
  'sys-scanner',
  'sys-users',
  'sys-channels',
  'sys-captcha',
  'sys-display',
] as const

export type AdminView = (typeof ADMIN_VIEWS)[number]

/** 管理侧落地页。旧书签 /admin 也落在此处。 */
export const ADMIN_HOME: AdminView = 'sys-console'

export function isAdminView(view: string): view is AdminView {
  return (ADMIN_VIEWS as readonly string[]).indexOf(view) >= 0
}

/*
canUseAdmin 决定侧栏管理组是否出现。三条件必须同时满足：
VITE_ADMIN_PANEL 编入管理界面、live 模式、角色为 operator 或 admin。
*/
export function canUseAdmin(role?: Role | string | null): boolean {
  return ADMIN_UI_ENABLED && IS_LIVE && (role === 'operator' || role === 'admin')
}
