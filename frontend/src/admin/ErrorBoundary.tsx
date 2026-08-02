/* 面板错误边界。
   单页渲染失败仅显示该页错误详情。
   禁止整站白屏。 */

import { Component, type ErrorInfo, type ReactNode } from 'react'
import { Btn } from './ui'

interface Props {
  children: ReactNode
  /** 出错时显示在标题里，标明失败页面 */
  label: string
  /** 重置键。切换标签页时更换，边界重新渲染。 */
  resetKey?: string
}

interface State {
  error: Error | null
  stack: string
}

export default class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null, stack: '' }

  static getDerivedStateFromError(error: Error): Partial<State> {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // 控制台保留完整堆栈。页面仅显示最有用的几行。
    console.error('[admin] 渲染失败:', error, info.componentStack)
    this.setState({ stack: (info.componentStack || '').trim().split('\n').slice(0, 6).join('\n') })
  }

  componentDidUpdate(prev: Props) {
    if (prev.resetKey !== this.props.resetKey && this.state.error) {
      this.setState({ error: null, stack: '' })
    }
  }

  render() {
    if (!this.state.error) return this.props.children
    return (
      <section style={{ padding: '48px 0' }}>
        <div style={{ fontSize: '22px', fontWeight: 600, letterSpacing: '-.03em' }}>
          「{this.props.label}」渲染失败
        </div>
        <div style={{ fontSize: '13px', color: 'var(--fg3)', lineHeight: 1.8, marginTop: '12px', maxWidth: '620px' }}>
          面板其余部分仍可用,请切换标签页。
        </div>
        <pre
          style={{
            marginTop: '20px',
            padding: '14px 16px',
            border: '1px solid var(--line)',
            font: "400 11.5px/1.7 'JetBrains Mono',monospace",
            color: 'var(--fg2)',
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-all',
            maxWidth: '760px',
          }}
        >
          {this.state.error.message}
          {this.state.stack ? '\n\n' + this.state.stack : ''}
        </pre>
        <div style={{ marginTop: '20px', display: 'flex', gap: '10px' }}>
          <Btn onClick={() => this.setState({ error: null, stack: '' })}>重试</Btn>
          <Btn onClick={() => window.location.reload()}>刷新整页</Btn>
        </div>
      </section>
    )
  }
}
