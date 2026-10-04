import { useEffect, useState } from 'react'
import { listUnread, type LiveEvents, type Unread } from '@/api/api.gen'
import { onEvent } from './events'

export type ChatKey = {
  organization: string
  repository: string
  workstream: number
}

export function sameChat(a: ChatKey, b: ChatKey) {
  return (
    a.organization === b.organization &&
    a.repository === b.repository &&
    a.workstream === b.workstream
  )
}

export function unreadCount(unread: Unread[], chat: ChatKey) {
  return unread.find((other) => sameChat(other, chat))?.count ?? 0
}

// The list is undefined until the first read of the counts.
export function useUnread(source?: EventSource) {
  const [unread, setUnread] = useState<Unread[]>()

  useEffect(() => {
    if (!source) {
      return
    }
    // The server sends an unread event only at a change, so each connection reads the counts.
    const load = () => {
      listUnread()
        .then((res) => {
          if (res.status === 200) {
            setUnread(res.data.data)
          }
        })
        .catch(() => {})
    }
    load()
    source.addEventListener('open', load)
    const remove = onEvent<LiveEvents, 'unread'>(source, 'unread', (count) =>
      setUnread((list = []) => [
        ...list.filter((other) => !sameChat(other, count)),
        count,
      ]),
    )
    return () => {
      source.removeEventListener('open', load)
      remove()
    }
  }, [source])

  return unread
}
