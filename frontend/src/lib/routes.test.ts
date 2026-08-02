import { resolveAppRoute, type AppRoute } from './routes'

function assertRoute(pathname: string, expected: AppRoute) {
  const actual = resolveAppRoute(pathname)
  if (actual !== expected) throw new Error(`${pathname || '(empty)'}: expected ${expected}, got ${actual}`)
}

export function runRouteTests() {
  assertRoute('', 'home')
  assertRoute('/', 'home')
  assertRoute('/admin', 'admin')
  assertRoute('/admin/', 'admin')
  assertRoute('/missing', 'not-found')
  assertRoute('/missing/', 'not-found')
  assertRoute('/admin/settings', 'not-found')
  assertRoute('/administrator', 'not-found')
}
