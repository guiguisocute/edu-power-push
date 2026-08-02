/* 注册邮箱域名白名单。纯前端校验，不发起后端请求。
   仅放行知名个人邮箱与教育域名后缀。其余一律拒绝。
   使用白名单：一次性域名无限增长，黑名单无法穷尽。
   本校验仅为注册前置过滤，非安全边界。
   账号有效性由后端邮箱验证保证。登录不执行此校验。 */

/** 知名个人邮箱服务商。精确匹配，全小写。 */
export const PROVIDER_DOMAINS: ReadonlySet<string> = new Set([
  // 中国大陆
  'qq.com',
  'vip.qq.com',
  'foxmail.com',
  '163.com',
  'vip.163.com',
  '126.com',
  'yeah.net',
  '188.com',
  'sina.com',
  'sina.cn',
  'vip.sina.com',
  'sohu.com',
  'vip.sohu.com',
  'aliyun.com',
  '139.com',
  '189.cn',
  'wo.cn',
  '21cn.com',
  '263.net',
  'tom.com',
  // 国际
  'gmail.com',
  'googlemail.com',
  'outlook.com',
  'hotmail.com',
  'live.com',
  'msn.com',
  'yahoo.com',
  'yahoo.co.jp',
  'ymail.com',
  'icloud.com',
  'me.com',
  'mac.com',
  'proton.me',
  'protonmail.com',
  'pm.me',
  'zoho.com',
  'gmx.com',
  'gmx.net',
  'aol.com',
  'yandex.com',
  'fastmail.com',
])

/** 教育机构域名后缀。后缀匹配，覆盖校园邮箱。 */
export const EDU_SUFFIXES: readonly string[] = [
  '.edu.cn',
  '.edu',
  '.ac.cn',
  '.edu.hk',
  '.edu.mo',
  '.edu.tw',
  '.edu.sg',
  '.edu.au',
  '.ac.uk',
  '.ac.jp',
  '.ac.kr',
]

export interface DomainCheck {
  ok: boolean
  msg: string
  /** 命中教育域名后缀。可用于校园邮箱标识。 */
  edu: boolean
}

/** 返回邮箱域名（小写）。格式非法返回 null。 */
export function emailDomain(email: string): string | null {
  const v = email.trim().toLowerCase()
  const at = v.lastIndexOf('@')
  if (at < 1 || at === v.length - 1) return null
  const d = v.slice(at + 1)
  if (!/^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$/.test(d)) return null
  if (!/\.[a-z]{2,}$/.test(d)) return null
  return d
}

/** 校验注册邮箱域名。仅在注册流程调用。 */
export function checkEmailDomain(email: string): DomainCheck {
  const d = emailDomain(email)
  if (!d) return { ok: false, msg: '请输入有效的邮箱地址', edu: false }
  if (EDU_SUFFIXES.some((s) => d.endsWith(s))) return { ok: true, msg: '', edu: true }
  if (PROVIDER_DOMAINS.has(d)) return { ok: true, msg: '', edu: false }
  return {
    ok: false,
    msg: `暂不支持 ${d} 注册。请使用 QQ、163、Gmail 等常用邮箱`,
    edu: false,
  }
}
