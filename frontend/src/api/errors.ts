/* 后端错误码映射为中文提示。
   error.code 为稳定机器可读值。未知 code 回落到后端 message。
   禁止吞掉错误。 */

import { ApiError } from './client'

const MESSAGES: Record<string, string> = {
  invalid_credentials: '邮箱或密码不正确',
  invalid_email: '邮箱格式不正确',
  invalid_password: '密码不符合要求（8–128 位）',
  invalid_nickname: '昵称最长 80 个字符',
  invalid_meter: '电表号应为 6–32 位数字',
  email_exists: '该邮箱已注册,请直接登录',
  email_not_verified: '请先完成邮箱验证码验证',
  registration_disabled: '注册暂未开放',
  // 第三方登录账号尚无密码。忘记密码流程用已验证邮箱设置密码。
  password_not_set: '账号尚未设置密码, 请用「忘记密码」功能通过邮箱验证码设置密码',
  meter_not_found: '电表号不在校园库存中',
  meter_not_supported: '该楼栋暂不支持绑定',
  meter_already_bound: '该电表绑定名额已满，最多 4 个账号',
  meter_not_bound: '请先绑定宿舍电表',
  unauthorized: '登录状态已失效，请重新登录',
  invalid_refresh_token: '登录状态已失效。请重新登录',
  refresh_reuse_detected: '检测到异常登录，已退出全部设备，请重新登录',
  rate_limited: '操作过于频繁，请稍后再试',
  auth_unavailable: '后端账号服务未启用',
  database_error: '后端数据服务异常，请稍后重试',
  captcha_required: '请先完成人机验证',
  captcha_invalid: '人机验证已失效，请重新验证',
  captcha_unavailable: '人机验证服务暂不可用，请稍后重试',
}

export function errMsg(e: unknown, fallback = '网络异常，请稍后重试'): string {
  if (e instanceof ApiError) {
    if (MESSAGES[e.code]) return MESSAGES[e.code]
    // 缺路由时返回能力不可用。禁止伪装为用户输入错误。
    if (isMissingEndpoint(e)) return '该功能后端尚未上线'
    return e.message || fallback
  }
  return fallback
}

/** 路由不存在。调用方据此选择降级路径。
    业务 404 带 error.code，不算缺端点。
    未注册路由的纯文本 404 记为 http_404。 */
export function isMissingEndpoint(e: unknown): boolean {
  return (
    e instanceof ApiError &&
    (e.status === 404 || e.status === 405 || e.status === 501) &&
    e.code.startsWith('http_')
  )
}
