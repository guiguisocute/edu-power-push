/* live 模式数据状态说明行。availability、quality、错误。红点加 mono 小字。 */

export default function LiveNote({ text, mt = '14px' }: { text: string; mt?: string }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: '9px', marginTop: mt }}>
      <span style={{ width: '5px', height: '5px', background: 'var(--red)', flex: 'none' }} />
      <span style={{ font: "400 10.5px/1 'JetBrains Mono',monospace", color: 'var(--fg3)' }}>{text}</span>
    </div>
  )
}
