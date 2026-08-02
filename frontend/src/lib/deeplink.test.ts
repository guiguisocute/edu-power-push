/* 由 smoke harness 直接 import 执行。不依赖 vitest/jest。 */
import {
  applyDeepLinkSearch,
  oauthReturnMessage,
  parseDeepLink,
  parseOAuthReturn,
  stripDeepLinkParams,
  stripOAuthParams,
} from './deeplink'

function assert(cond: unknown, msg: string) {
  if (!cond) throw new Error(msg)
}

export function runDeepLinkTests() {
  assert(parseDeepLink('?reset=1').kind === 'forgot_password', 'reset=1')
  assert(parseDeepLink('reset=1').kind === 'forgot_password', 'reset=1 bare')
  assert(parseDeepLink('?reset=true').kind === 'forgot_password', 'reset=true')
  assert(parseDeepLink('?reset').kind === 'forgot_password', 'reset alone')
  assert(parseDeepLink('?reset=0').kind === 'none', 'reset=0 ignored')
  assert(parseDeepLink('').kind === 'none', 'empty')
  assert(parseDeepLink('?api=mock').kind === 'none', 'other query')

  assert(stripDeepLinkParams('?reset=1') === '', 'strip only reset')
  assert(stripDeepLinkParams('?reset=1&api=mock') === '?api=mock', 'keep api')
  assert(stripDeepLinkParams('?api=mock&reset=true') === '?api=mock', 'order independent')

  const applied = applyDeepLinkSearch('?reset=1&api=live')
  assert(applied.openForgot === true, 'apply opens forgot')
  assert(applied.nextSearch === '?api=live', 'apply strips reset')

  /* 第三方登录回跳 */
  const ok = parseOAuthReturn('?oauth=google&oauth_result=created')
  assert(ok.kind === 'success', 'oauth success')
  assert(ok.kind === 'success' && ok.provider === 'google', 'oauth provider')
  assert(oauthReturnMessage(ok).indexOf('Google') >= 0, 'oauth message names the provider')

  const failed = parseOAuthReturn('?oauth_error=denied')
  assert(failed.kind === 'error', 'oauth error')
  assert(oauthReturnMessage(failed) === '已取消第三方登录', 'oauth denied message')
  // 未知原因码必须映射可读提示。禁止把码本身丢给用户。
  assert(oauthReturnMessage({ kind: 'error', reason: 'who_knows' }).indexOf('失败') >= 0, 'oauth fallback message')

  // oauth_error 优先于 oauth。两者同时存在时视为失败。
  assert(parseOAuthReturn('?oauth=github&oauth_error=state').kind === 'error', 'error wins')

  assert(parseOAuthReturn('?api=mock').kind === 'none', 'no oauth params')
  assert(stripOAuthParams('?oauth=google&oauth_result=linked&api=live') === '?api=live', 'strip oauth params')
  assert(stripOAuthParams('?oauth_error=state') === '', 'strip lone oauth error')
}
