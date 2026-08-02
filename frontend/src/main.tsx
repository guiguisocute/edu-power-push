import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './styles/fonts.css'
import './styles/global.css'
import App from './App'
import NotFound from './components/NotFound'
import { resolveAppRoute } from './lib/routes'
import { ADMIN_HOME } from './lib/adminAccess'

/* 站点仅一个应用外壳。管理界面并入主站侧栏。
   /admin 为旧书签入口：地址改回 /，初始视图落到控制台。
   无权限时由 App 守卫退回概览。其他路径返回 404。 */
const route = resolveAppRoute(window.location.pathname)

if (route === 'admin') {
  window.history.replaceState(null, '', '/' + window.location.search + window.location.hash)
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    {route === 'not-found' ? <NotFound /> : <App initialView={route === 'admin' ? ADMIN_HOME : undefined} />}
  </StrictMode>,
)
