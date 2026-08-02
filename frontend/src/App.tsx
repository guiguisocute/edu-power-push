/* 应用外壳。结构对齐原型 shell。 */

import Sidebar from './components/Sidebar'
import { TopProgress } from './components/Loading'
import TopBar from './components/TopBar'
import AuthOverlay from './components/AuthOverlay'
import { Gate, Toast } from './components/GateToast'
import OverviewView from './views/OverviewView'
import UsageView from './views/UsageView'
import CampusView from './views/CampusView'
import BoardView from './views/BoardView'
import ConfigView from './views/ConfigView'
import { StoreCtx, useCreateStore, type View } from './lib/store'
import { DEFAULT_BRAND_NAME, DEFAULT_CAMPUS_NAME, fetchRemoteFeatures } from './config/features'
import { applyDeepLinkSearch, oauthReturnMessage, parseOAuthReturn, stripOAuthParams } from './lib/deeplink'
import { canUseAdmin, isAdminView } from './lib/adminAccess'
import { IS_LIVE } from './api/mode'
import { lazy, Suspense, useEffect } from 'react'

/* 管理界面懒加载。仅运维使用。避免普通用户下载多余代码。 */
const AdminSection = lazy(() => import('./admin/AdminSection'))

export default function App({ initialView }: { initialView?: View } = {}) {
  const store = useCreateStore(initialView)
  const s = store.s
  /* 仅个人数据视图必须登录。数据看板与排行榜允许匿名。campus/* 为匿名端点。 */
  const signedOut = !s.user && ['overview', 'usage', 'config', 'account'].indexOf(s.view) >= 0
  /* 管理页不用 Gate 遮罩。无权限时直接切换视图。 */
  const adminView = isAdminView(s.view) && canUseAdmin(s.user?.role) ? s.view : null
  /* 已登录且未绑表时，用电分析与推送配置使用 Gate。概览在 hero 内联绑表。 */
  const unbound = !!s.user && !s.user.meter && ['usage', 'config'].indexOf(s.view) >= 0
  const locked = signedOut || unbound
  // 会话恢复中仅模糊。禁止弹出 Gate，避免刷新闪登录提示。
  const booting = !s.sessionReady

  // 启动时查询运维前端配置。失败时使用默认值。
  useEffect(() => {
    if (!IS_LIVE) return
    let alive = true
    fetchRemoteFeatures().then((f) => {
      // 深合并后兜底 charts 与 display。缺键会导致视图白屏。
      if (!alive) return
      store.set({
        features: {
          ...f,
          charts: { ...{ dayRange: false, hourlyUsage: false }, ...f.charts },
          display: {
            ...{
              electricityRate: '0.62',
              campusName: DEFAULT_CAMPUS_NAME,
              areaName: '',
              brandName: DEFAULT_BRAND_NAME,
              rankingRefreshTime: '09:00',
            },
            ...f.display,
          },
        },
      })
    })
    return () => {
      alive = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  /* 页面标题跟随运维配置。index.html 仅有中性标题。配置到达后在此更新。 */
  useEffect(() => {
    if (typeof document === 'undefined') return
    const { areaName, brandName } = s.features.display
    const site = brandName || DEFAULT_BRAND_NAME
    document.title = areaName ? `电费推送面板 · ${areaName}` : `电费推送面板 · ${site}`
  }, [s.features.display.areaName, s.features.display.brandName])

  /* 管理视图兜底守卫。sessionReady 为 false 时禁止切换。避免刷新踢回概览。 */
  useEffect(() => {
    if (s.sessionReady && isAdminView(s.view) && !canUseAdmin(s.user?.role)) store.set({ view: 'overview' })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [s.view, s.sessionReady, s.user?.role])

  /* 深链 /?reset=1 打开重置密码浮层。然后清除 query，避免刷新重复打开。 */
  useEffect(() => {
    if (typeof window === 'undefined') return
    const { openForgot, nextSearch } = applyDeepLinkSearch(window.location.search)
    if (!openForgot) return
    store.set({ authView: 'forgot', authErr: null, codeSent: false })
    const path = window.location.pathname + nextSearch + window.location.hash
    window.history.replaceState({}, '', path)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  /* 第三方登录回跳结果在 ?oauth= 与 ?oauth_error=。
     会话由 refresh cookie 与 bootSession 恢复。
     此处仅提示结果并清除参数。失败时打开登录浮层。 */
  useEffect(() => {
    if (typeof window === 'undefined') return
    const outcome = parseOAuthReturn(window.location.search)
    if (outcome.kind === 'none') return
    const nextSearch = stripOAuthParams(window.location.search)
    window.history.replaceState({}, '', window.location.pathname + nextSearch + window.location.hash)
    store.say(oauthReturnMessage(outcome), 6000)
    if (outcome.kind === 'error') store.set({ authView: 'login', authErr: oauthReturnMessage(outcome) })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return (
    <StoreCtx.Provider value={store}>
      <TopProgress />
      <div
        data-r="shell"
        style={{
          display: 'flex',
          minHeight: '100vh',
          background: 'var(--bg)',
          color: 'var(--fg)',
          fontFamily: "'Instrument Sans','Noto Sans SC',system-ui,sans-serif",
          fontSize: '13px',
          lineHeight: 1.5,
        }}
      >
        <Sidebar />
        <main style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column' }}>
          <TopBar />
          <div data-r="pad" style={{ padding: '0 44px 110px', flex: 1, position: 'relative' }}>
            {locked && !booting && <Gate mode={unbound ? 'bind' : 'signin'} />}
            <div
              style={{
                filter: `blur(${locked ? '6px' : '0px'})`,
                transition: 'filter .25s ease',
                pointerEvents: locked ? 'none' : 'auto',
                userSelect: locked ? 'none' : 'auto',
              }}
            >
              {s.view === 'overview' && <OverviewView />}
              {s.view === 'usage' && <UsageView />}
              {s.view === 'campus' && <CampusView />}
              {s.view === 'board' && <BoardView />}
              {s.view === 'config' && <ConfigView section="push" />}
              {s.view === 'account' && <ConfigView section="account" />}
              {adminView && (
                <Suspense fallback={null}>
                  <AdminSection view={adminView} />
                </Suspense>
              )}
            </div>
          </div>
        </main>
        {s.mobileNavOpen && (
          <div className="m-only-block">
            <div
              onClick={() => store.set({ mobileNavOpen: false, menuOpen: false })}
              style={{ position: 'fixed', inset: 0, zIndex: 68, background: 'rgba(0,0,0,.32)' }}
            />
            <Sidebar variant="drawer" />
          </div>
        )}
        <AuthOverlay />
        <Toast />
      </div>
    </StoreCtx.Provider>
  )
}
