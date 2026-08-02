/* 扫描状态与计数器守卫。与后端契约对齐。
   防止面板漏认状态或读错计数器字段。
   断言直接读 openapi.yaml，禁止另抄一份枚举常量。 */
/* 用 ?raw 在构建期内联契约。禁止运行时 fs.readFileSync。
   harness 打包后相对路径已变。 */
import openapiSpec from '../../../backend/api/openapi.yaml?raw'
import { formatRunDuration, STATUS_LABEL, STATUS_TONE, summarize } from './ScannerView'
import type { ScanCounters, ScanRun } from './api'

function assert(cond: unknown, msg: string) {
  if (!cond) throw new Error(msg)
}

/** 从 openapi.yaml 取出 ScanRunStatus 枚举取值 */
function contractStatuses(): string[] {
  const m = openapiSpec.match(/ScanRunStatus:\s*\n\s*type:\s*string\s*\n\s*enum:\s*\[([^\]]+)\]/)
  assert(m, 'openapi.yaml 里找不到 ScanRunStatus 枚举')
  return m![1].split(',').map((s) => s.trim()).filter(Boolean)
}

const ZERO: ScanCounters = {
  inventory_total: 0,
  excluded_total: 0,
  eligible_total: 0,
  processed_total: 0,
  valid_total: 0,
  stale_total: 0,
  empty_total: 0,
  error_total: 0,
  parse_error_total: 0,
  duplicate_reading_total: 0,
}

function run(over: Partial<ScanRun> = {}): ScanRun {
  return {
    id: 'r1',
    trigger: 'manual',
    status: 'completed',
    started_at: '2026-07-27T12:15:00Z',
    qps: 4,
    concurrency: 4,
    bound_only: false,
    counters: ZERO,
    ...over,
  }
}

export function runScanStatusTests() {
  const statuses = contractStatuses()
  assert(statuses.length >= 6, `枚举只解析出 ${statuses.length} 个取值,请检查正则是否匹配。`)

  // 后端每个状态都要有中文标签和颜色，一个都不能漏
  for (const st of statuses) {
    assert(STATUS_LABEL[st], `状态 ${st} 缺中文标签（会原样显示英文并撑破版面）`)
    assert(STATUS_TONE[st], `状态 ${st} 缺颜色`)
    // 标签太长同样会挤坏 112px 的列
    assert(STATUS_LABEL[st].length <= 7, `状态 ${st} 的标签「${STATUS_LABEL[st]}」过长`)
  }

  // 反向：面板不该认识后端没有的状态（写错字母时能被抓到）
  for (const st of Object.keys(STATUS_LABEL)) {
    assert(statuses.includes(st), `面板认了后端没有的状态 ${st}`)
  }

  // 计数器摘要必须真的读到数，而不是恒为 0
  const done = summarize(
    run({ status: 'completed_with_errors', counters: { ...ZERO, valid_total: 6269, stale_total: 1249, parse_error_total: 1 } }),
  )
  assert(done.includes('6269'), `摘要没读到 valid_total：${done}`)
  assert(done.includes('1249'), `摘要没读到 stale_total：${done}`)
  assert(done.includes('异常 1'), `摘要没合并 error/parse_error：${done}`)

  // 在途轮次看进度，不是最终计数。
  const running = summarize(run({ status: 'running', counters: { ...ZERO, processed_total: 3200, eligible_total: 7519 } }))
  assert(running.includes('3200') && running.includes('7519'), `进行中摘要不对：${running}`)

  // 在途耗时应随时间增长。结束后冻结在 finished_at。
  const activeDuration = formatRunDuration(
    run({ status: 'running', started_at: '2026-07-27T12:15:00Z' }),
    new Date('2026-07-27T12:16:05Z').getTime(),
  )
  assert(activeDuration === '01:05', `进行中耗时不对：${activeDuration}`)

  const completedDuration = formatRunDuration(
    run({
      status: 'completed',
      started_at: '2026-07-27T12:15:00Z',
      finished_at: '2026-07-27T13:17:03Z',
    }),
    new Date('2026-07-28T12:15:00Z').getTime(),
  )
  assert(completedDuration === '1:02:03', `已结束耗时没有按 finished_at 冻结：${completedDuration}`)

  const interruptedDuration = formatRunDuration(run({
    status: 'interrupted',
    started_at: '2026-07-27T12:15:00Z',
    heartbeat_at: '2026-07-27T12:17:00Z',
  }))
  assert(interruptedDuration === '02:00', `中断轮次应回退到最后心跳：${interruptedDuration}`)

  // 老记录缺字段时禁止抛错。
  const legacy = summarize(run({ counters: undefined as unknown as ScanCounters }))
  assert(legacy.includes('0'), `缺计数器时应兜底成 0：${legacy}`)
}
