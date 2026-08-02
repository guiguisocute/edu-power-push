/* 冒烟 harness。真渲染关键视图并断言文案、深链与隐私逻辑。
   tsc 抓不到运行时空指针与未渲染按钮。用 renderToString 加字符串断言兜住。 */
import { renderToString } from 'react-dom/server'
import { StoreCtx, initialState, type Store, type AppState } from './lib/store'
import OverviewView from './views/OverviewView'
import UsageView from './views/UsageView'
import CampusView from './views/CampusView'
import BoardView from './views/BoardView'
import ConfigView from './views/ConfigView'
import AuthOverlay from './components/AuthOverlay'
import InlineBindMeter from './components/InlineBindMeter'
import { Gate } from './components/GateToast'
import Sidebar from './components/Sidebar'
import TopBar from './components/TopBar'
import AccountMenu from './components/AccountMenu'
import PushHistory from './components/PushHistory'
import AdminSection from './admin/AdminSection'
import ScannerView from './admin/ScannerView'
import OAuthCredentials from './admin/OAuthCredentials'
import { runDeepLinkTests } from './lib/deeplink.test'
import { runPrivacyTests } from './lib/privacy.test'
import { runScanStatusTests } from './admin/scanstatus.test'
import { runMonthKeyTests } from './lib/monthkeys.test'
import { runMonthTests } from './lib/months.test'
import { runSemesterTests } from './lib/semesters.test'
import { runPushLogTests } from './lib/pushLogs.test'
import { runUsageExportTests } from './lib/exportUsage.test'
import { runFormatTests } from './lib/format.test'
import { runSessionTests } from './api/session.test'
import { runRankingPeriodTests } from './lib/rankingPeriods.test'
import { runBalanceChartTests } from './lib/balanceChart.test'
import { runChannelPresentationTests } from './lib/channels.test'
import { runRouteTests } from './lib/routes.test'
import { runNotificationSettingsTests } from './api/notifications.test'
import { DEFAULT_FEATURES } from './config/features'
import NotFound from './components/NotFound'
import { runCaptchaGateTests } from './components/captchaGate.test'
import { runCampusYearTests } from './lib/campusYear.test'

const noop = () => {}
let failed = 0

function fail(msg: string) {
  console.log('  FAIL ' + msg)
  failed++
}

function ok(msg: string) {
  console.log('  OK   ' + msg)
}

function render(
  name: string,
  node: React.ReactNode,
  over: Partial<AppState>,
  mustInclude: string[] = [],
  mustExclude: string[] = [],
) {
  const s = { ...initialState, ...over }
  const store = {
    s,
    set: noop,
    say: noop,
    persist: noop,
    applyTheme: noop,
    setForm: noop,
    startBind: noop,
    lookupMeter: noop,
    doLogin: noop,
    doOAuth: noop,
    doRegister: noop,
    sendCode: noop,
    doReset: noop,
    doRebind: noop,
    doLogout: noop,
    sendRegisterCode: noop,
    setPrivacy: noop,
    setNotifSettings: noop,
    resetPushDefaults: noop,
    syncChannel: noop,
    testPushChannel: noop,
    saveNickname: async () => false,
    changePassword: async () => false,
    sendEmailChangeCode: async () => false,
    changeEmail: async () => false,
  } as unknown as Store
  try {
    const html = renderToString(<StoreCtx.Provider value={store}>{node}</StoreCtx.Provider>)
    for (const needle of mustInclude) {
      if (!html.includes(needle)) {
        fail(`${name} 缺少文案「${needle}」`)
        return
      }
    }
    // 排除项与包含项同权重。管理入口漏给普通用户是安全问题。
    for (const needle of mustExclude) {
      if (html.includes(needle)) {
        fail(`${name} 不该出现文案「${needle}」`)
        return
      }
    }
    ok(name + (mustInclude.length ? `  (含 ${mustInclude.join(' / ')})` : ''))
  } catch (e) {
    fail(name + '  ->  ' + (e as Error).message)
  }
}

console.log('单元 · 深链与隐私')
try {
  runCaptchaGateTests()
  ok('CAPTCHA 异步切换不改变 hook 顺序')
} catch (e) {
  fail('CAPTCHA hook 顺序 · ' + (e as Error).message)
}
try {
  runDeepLinkTests()
  ok('deeplink parse/strip/apply')
} catch (e) {
  fail('deeplink · ' + (e as Error).message)
}
try {
  runRouteTests()
  ok('site routes + shared 404')
} catch (e) {
  fail('site routes · ' + (e as Error).message)
}
try {
  runNotificationSettingsTests()
  ok('notification template API mapping + legacy defaults')
} catch (e) {
  fail('notification templates · ' + (e as Error).message)
}
try {
  runPrivacyTests()
  ok('privacy structured location + mask')
} catch (e) {
  fail('privacy · ' + (e as Error).message)
}
try {
  runScanStatusTests()
  ok('扫描状态枚举与后端 openapi 对齐 + 计数器摘要 + 轮次耗时')
} catch (e) {
  fail('scan status · ' + (e as Error).message)
}
try {
  runMonthKeyTests()
  ok('账期下拉覆盖全部有账单的月份')
} catch (e) {
  fail('month keys · ' + (e as Error).message)
}
try {
  runMonthTests()
  ok('月份区间算术：跨年 / 月末 / 端点夹取 / 下拉连续')
} catch (e) {
  fail('months · ' + (e as Error).message)
}
try {
  runCampusYearTests()
  ok('月账单未发布时保留最近有效统计，不追加伪 0 柱/空热力图列')
} catch (e) {
  fail('campus year fallback · ' + (e as Error).message)
}
try {
  runSemesterTests()
  ok('学期切周：本地时区 / 非周一开学 / 不足一周的尾巴 / 缺数不算 0')
} catch (e) {
  fail('semesters · ' + (e as Error).message)
}
try {
  runPushLogTests()
  ok('真实推送记录：时区 / 渠道 / 投递状态 / 失败原因')
} catch (e) {
  fail('push logs · ' + (e as Error).message)
}
try {
  runUsageExportTests()
  ok('用电详情导出：两序列对齐 / 缺数留空 / CSV BOM 与转义 / JSON 元信息')
} catch (e) {
  fail('usage export · ' + (e as Error).message)
}
try {
  runFormatTests()
  ok('用电构成：总量列数值与动态表头单位一致')
} catch (e) {
  fail('consumption mix units · ' + (e as Error).message)
}
try {
  runSessionTests()
  ok('会话失效广播 + 排行榜缓存按账号隔离')
} catch (e) {
  fail('session · ' + (e as Error).message)
}
try {
  runRankingPeriodTests()
  runBalanceChartTests()
  runChannelPresentationTests()
  ok('排行榜真实周期 + 余额图指针吸附 + 渠道展示状态')
} catch (e) {
  fail('chart interactions · ' + (e as Error).message)
}

const BOUND = {
  ...initialState.user!,
  building: '11栋',
  floor: '1楼',
  room: 'N301',
  place: '11栋 · 1楼 · N301',
  email: 'user@example.com',
}
const UNBOUND = {
  name: '同学1111',
  initial: '同',
  meter: null,
  place: null,
  phone: '1***@qq.com',
  email: '1@qq.com',
}
const states: [string, Partial<AppState>][] = [
  ['未登录', { user: null }],
  ['已登录·未绑表', { user: UNBOUND as AppState['user'] }],
  ['已登录·已绑表', { user: BOUND as AppState['user'] }],
]
const views: [string, React.ReactNode, string[]][] = [
  ['概览', <OverviewView />, ['最近推送']],
  ['用电分析', <UsageView />, []],
  /* 用电构成与热力图都必须在页面上。
     标题有插值时 renderToString 会插注释节点，只断言前半段。 */
  ['数据看板', <CampusView />, ['用电构成 · 按', 'BUILDING RANK', 'FLOOR RANK', 'TRENDS · OVER TIME', '走势对比 · 按', 'LOAD HEATMAP', '热力图 · ']],
  ['排行榜', <BoardView />, []],
  ['推送设置', <ConfigView section="push" />, ['低额度预警', '定时推送', '推送渠道', '开箱即用', '群聊机器人', '进阶开发者渠道', '消息模板', '企业微信消息推送', 'Discord 群组 Webhook', '所有改动已自动']],
  ['账号设置', <ConfigView section="account" />, []],
  ['侧栏', <Sidebar />, []],
  ['顶栏', <TopBar />, []],
  ['账号菜单', <AccountMenu />, []],
]
for (const [sn, so] of states) {
  console.log(sn)
  for (const [vn, node, needles] of views) {
    // 已登录绑表时账号页必须出现账号与隐私设置
    const extra =
      sn === '已登录·已绑表' && vn === '账号设置'
        ? ['账号设置', 'NICKNAME', 'EMAIL', 'PASSWORD', 'METER', '隐私设置', '房间号',
           // 账号设置右栏：填满宽度的账号状态卡，不是留白
           'ACCOUNT STATUS', '登录中的设备', '登出所有设备',
           // 注销入口必须在账号设置里露出（面板本身要点开才展开）
           'DELETE ACCOUNT', '注销账号']
        : needles
    render(vn, node, so, extra)
  }
}

console.log('浮层')
render('全站 404', <NotFound />, {}, ['404', '页面不存在'])
/* 概览页在 SSR（production 模式 = live）下拿不到真实日志，只会渲染空态，
   所以推送历史的折叠/展开与「点开看技术详情」这条路要单独喂一份记录来验。 */
const pushSamples = Array.from({ length: 7 }, (_, i) => ({
  id: 'sample-' + i,
  sent_at: new Date(Date.parse('2026-07-25T17:42:00+08:00') - i * 3600_000).toISOString(),
  channel: i % 2 ? 'dingtalk' : 'mail',
  kind: (i === 3 ? 'test' : 'digest') as 'test' | 'digest',
  status: (i === 1 ? 'failed' : 'delivered') as 'failed' | 'delivered',
  summary: '定时摘要 · 余额 43.87 元',
  error: i === 1 ? 'i/o timeout' : null,
}))
render(
  '推送历史·折叠与详情入口',
  <PushHistory logs={pushSamples} colors={{ ok: '#12a150', error: '#e5242a', muted: '#888' }} />,
  {},
  ['查看技术详情', '已显示 5 条', '查看更早的记录', '钉钉群机器人', '失败 · i/o timeout'],
)
const featuresWithReset = {
  ...DEFAULT_FEATURES,
  auth: { ...DEFAULT_FEATURES.auth, emailCode: true, emailLogin: true },
}
render('登录·应有忘记密码', <AuthOverlay />, { user: null, authView: 'login', features: featuresWithReset }, [
  '忘记密码？',
  '登录',
  'PASSWORD',
  '显示密码',
  'type="submit"',
])
render('注册', <AuthOverlay />, { user: null, authView: 'register' }, [
  '创建账号',
  '发送验证码',
  '下一步',
  '使用 Google 注册',
])
/* 第三方登录：两开关都开时登录页应有两入口。
   服务端无凭证时禁止出现入口。必然失败的按钮比不画更差。 */
render('登录·第三方入口', <AuthOverlay />, { user: null, authView: 'login', features: DEFAULT_FEATURES }, [
  '使用 Google 登录',
  '使用 GitHub 登录',
  'OR',
])
render(
  '登录·后端未配 OAuth 时不出现入口',
  <AuthOverlay />,
  {
    user: null,
    authView: 'login',
    features: { ...DEFAULT_FEATURES, oauth: { google: false, github: false } },
  },
  ['登录'],
  ['使用 Google 登录', '使用 GitHub 登录'],
)
render(
  '找回密码·不该出现第三方入口',
  <AuthOverlay />,
  { user: null, authView: 'forgot', features: DEFAULT_FEATURES },
  ['重置密码'],
  ['使用 Google 登录'],
)
render('找回密码·邮件深链目标页', <AuthOverlay />, { user: null, authView: 'forgot' }, [
  '重置密码',
  '发送验证码',
  'NEW PASSWORD',
  '显示密码',
])
render(
  '内联绑表·输入',
  <InlineBindMeter />,
  { user: UNBOUND as AppState['user'], bindOpen: true },
  [],
)
render(
  '内联绑表·已匹配',
  <InlineBindMeter />,
  {
    user: UNBOUND as AppState['user'],
    bindOpen: true,
    matched: { no: '31240718', place: '河东 12 栋 · 4 楼 · 402' },
  },
  [],
)
render('Gate·绑表', <Gate mode="bind" />, { user: UNBOUND as AppState['user'], view: 'config' })
render('Gate·登录', <Gate />, { user: null, view: 'config' })

// 隐私预览：结构化位置禁止把房间揉进楼栋。
console.log('配置页隐私预览文案')
render(
  '隐私·结构化位置',
  <ConfigView section="account" />,
  {
    user: BOUND as AppState['user'],
    joinBoard: true,
    pf: { meter: false, bldg: true, floor: true, room: true, nick: false },
    pmask: { meter: true, bldg: false, floor: false, room: false },
  },
  ['11栋', '1楼', 'N301'],
)

/* 管理侧为主站侧栏选项卡。与其他视图共用渲染冒烟。
   普通账号禁止看见系统管理。运维与管理员必须看见。
   smoke 走 production 构建条件：VITE_API_MODE=live、VITE_ADMIN_PANEL=true。 */
console.log('管理侧')
const OPERATOR = { ...BOUND, role: 'operator' as const, email: 'ops@example.com' }
render('侧栏·普通账号看不到管理组', <Sidebar />, { user: BOUND as AppState['user'] }, [], ['系统管理'])
render(
  '侧栏·运维账号有管理组',
  <Sidebar />,
  { user: OPERATOR as AppState['user'] },
  ['系统管理', 'OPERATOR', '控制台', '扫描器', '用户管理', '人机验证', '前端展示'],
)
/* 权限校验返回前禁止渲染。renderToString 不跑 effect，可见该中间态。 */
render(
  '管理内容区·校验权限时不抛异常',
  <AdminSection view="sys-console" />,
  { user: OPERATOR as AppState['user'] },
  ['正在校验管理权限'],
)
render(
  '三类扫描器控制台·首屏结构',
  <ScannerView canWrite onError={noop} onToast={noop} />,
  {},
  ['余额扫描', '官方日明细扫描', '月账单扫描', '统一扫描配置'],
)
/* 第三方登录 client secret 与邮件、人机验证同级。
   运维仅见说明，不能看也不能改密钥。对齐后端 adminRoute。 */
render(
  '第三方登录凭证·运维看不到密钥',
  <OAuthCredentials canManage={false} onError={noop} onToast={noop} onSaved={noop} />,
  {},
  ['第三方登录凭证', '仅管理员'],
  ['GOOGLE CLIENT SECRET', 'GITHUB CLIENT SECRET'],
)

if (failed) {
  console.log('\n' + failed + ' 项失败')
  throw new Error(failed + ' 个 harness 断言失败')
}
console.log('\n全部通过')
