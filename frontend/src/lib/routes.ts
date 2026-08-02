/* 站点路由。结果仅三种：主应用、/admin 旧书签、404。
   admin 仅表示初始落地页，不表示权限。权限由账号角色决定。 */

export type AppRoute = 'home' | 'admin' | 'not-found'

export function resolveAppRoute(pathname: string): AppRoute {
  if (pathname === '' || pathname === '/') return 'home'
  if (pathname.replace(/\/+$/, '') === '/admin') return 'admin'
  return 'not-found'
}
