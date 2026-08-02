/* 第三方登录入口。登录页与注册第一步共用。
   放在邮箱表单上方。OR 线说明两条路通向同一账号。
   图标为内联 SVG。CSP 为 default-src self，禁止外链图标。
   按钮为次级动作：细边框加药丸形。 */

import type { OAuthProvider } from '../api/client'
import { enabledOAuthProviders } from '../config/features'
import { useStore } from '../lib/store'
import { providerLabel } from '../lib/deeplink'

function GoogleMark() {
  return (
    <svg width="16" height="16" viewBox="0 0 48 48" aria-hidden="true" style={{ flex: 'none' }}>
      <path
        fill="#4285F4"
        d="M45.12 24.5c0-1.56-.14-3.06-.4-4.5H24v8.51h11.84c-.51 2.75-2.06 5.08-4.39 6.64v5.52h7.11c4.16-3.83 6.56-9.47 6.56-16.17z"
      />
      <path
        fill="#34A853"
        d="M24 46c5.94 0 10.92-1.97 14.56-5.33l-7.11-5.52c-1.97 1.32-4.49 2.1-7.45 2.1-5.73 0-10.58-3.87-12.31-9.07H4.34v5.7C7.96 41.07 15.4 46 24 46z"
      />
      <path
        fill="#FBBC05"
        d="M11.69 28.18C11.25 26.86 11 25.45 11 24s.25-2.86.69-4.18v-5.7H4.34C2.85 17.09 2 20.45 2 24s.85 6.91 2.34 9.88l7.35-5.7z"
      />
      <path
        fill="#EA4335"
        d="M24 10.75c3.23 0 6.13 1.11 8.41 3.29l6.31-6.31C34.91 4.18 29.93 2 24 2 15.4 2 7.96 6.93 4.34 14.12l7.35 5.7c1.73-5.2 6.58-9.07 12.31-9.07z"
      />
    </svg>
  )
}

function GitHubMark() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" aria-hidden="true" style={{ flex: 'none' }}>
      <path
        fill="currentColor"
        d="M12 .5C5.73.5.5 5.73.5 12c0 5.08 3.29 9.39 7.86 10.91.58.11.79-.25.79-.56 0-.28-.01-1.02-.02-2-3.2.7-3.88-1.54-3.88-1.54-.52-1.33-1.28-1.68-1.28-1.68-1.05-.72.08-.7.08-.7 1.16.08 1.77 1.19 1.77 1.19 1.03 1.77 2.7 1.26 3.36.96.1-.75.4-1.26.73-1.55-2.55-.29-5.24-1.28-5.24-5.69 0-1.26.45-2.29 1.19-3.09-.12-.29-.52-1.46.11-3.05 0 0 .97-.31 3.18 1.18a11 11 0 0 1 5.79 0c2.2-1.49 3.17-1.18 3.17-1.18.63 1.59.23 2.76.11 3.05.74.8 1.19 1.83 1.19 3.09 0 4.42-2.69 5.39-5.25 5.68.41.36.78 1.06.78 2.14 0 1.55-.01 2.8-.01 3.18 0 .31.21.68.8.56C20.21 21.38 23.5 17.08 23.5 12 23.5 5.73 18.27.5 12 .5z"
      />
    </svg>
  )
}

const MARK: Record<OAuthProvider, () => JSX.Element> = {
  google: GoogleMark,
  github: GitHubMark,
}

export default function OAuthButtons({ intent }: { intent: 'login' | 'register' }) {
  const { s, doOAuth } = useStore()
  const providers = enabledOAuthProviders(s.features)
  if (!providers.length) return null
  const verb = intent === 'register' ? '注册' : '登录'

  return (
    <div style={{ display: 'flex', flexDirection: 'column', marginTop: '28px' }}>
      {providers.map((provider) => {
        const Mark = MARK[provider]
        return (
          <button
            key={provider}
            type="button"
            className="hv-line-fg"
            disabled={s.authBusy}
            onClick={() => doOAuth(provider)}
            style={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              gap: '10px',
              margin: '0 0 10px',
              padding: '13px 20px',
              border: '1px solid var(--line)',
              borderRadius: '999px',
              background: 'none',
              font: 'inherit',
              fontSize: '13px',
              fontWeight: 500,
              color: 'var(--fg)',
              cursor: s.authBusy ? 'default' : 'pointer',
              opacity: s.authBusy ? 0.6 : 1,
              transition: 'border-color .18s ease, opacity .2s ease',
            }}
          >
            <Mark />
            {/* 拼成一个字符串再渲染：分段插值会被 React 切成多个文本节点，
                服务端渲染时中间还会插进注释，按整句查找的断言就再也匹配不上。 */}
            {`使用 ${providerLabel(provider)} ${verb}`}
          </button>
        )
      })}

      {/* 两条路通向同一个账号：已验证的同一个邮箱会落到同一个号上 */}
      <div style={{ display: 'flex', alignItems: 'center', gap: '12px', margin: '12px 0 4px' }}>
        <div style={{ flex: 1, height: '1px', background: 'var(--line2)' }} />
        <span
          style={{ font: "500 9px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}
        >
          OR
        </span>
        <div style={{ flex: 1, height: '1px', background: 'var(--line2)' }} />
      </div>
    </div>
  )
}
