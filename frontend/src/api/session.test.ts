import { setAccessToken, subscribeAccessToken } from './client'
import { sessionCacheScope, type SessionState } from './session'
import type { User } from '../lib/mock'

export function runSessionTests() {
  const anon: SessionState = { status: 'anon', user: null }
  const user = (email: string) => ({ email }) as User
  const first: SessionState = { status: 'signed-in', user: user('first@example.com') }
  const second: SessionState = { status: 'signed-in', user: user('second@example.com') }

  if (sessionCacheScope(anon) === sessionCacheScope(first)) {
    throw new Error('anonymous and signed-in ranking caches share a scope')
  }
  if (sessionCacheScope(first) === sessionCacheScope(second)) {
    throw new Error('different accounts share a ranking cache scope')
  }

  const seen: Array<string | null> = []
  const unsubscribe = subscribeAccessToken((token) => seen.push(token))
  setAccessToken('test-token')
  setAccessToken(null)
  unsubscribe()
  if (seen.length !== 2 || seen[0] !== 'test-token' || seen[1] !== null) {
    throw new Error('access-token invalidation was not broadcast')
  }
}
