export default function NotFound() {
  return (
    <div
      style={{
        minHeight: '100vh',
        background: 'var(--bg)',
        color: 'var(--fg)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        padding: '40px',
      }}
    >
      <div style={{ textAlign: 'center' }}>
        <div style={{ font: "500 12px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}>
          404
        </div>
        <div style={{ fontSize: '15px', color: 'var(--fg2)', marginTop: '14px' }}>页面不存在</div>
      </div>
    </div>
  )
}
