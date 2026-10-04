import { useEffect, useState } from 'react'
import { listInbox, type InboxItem, type LiveEvents } from '@/api/api.gen'
import { onEvent } from './events'

export function useInbox(source?: EventSource) {
  const [items, setItems] = useState<InboxItem[]>([])

  useEffect(() => {
    if (!source) {
      return
    }
    // An Inbox event that comes while the connection is down is lost, so each connection reads the items.
    const load = () => {
      listInbox()
        .then((res) => {
          if (res.status === 200) {
            setItems(res.data.data)
          }
        })
        .catch(() => {})
    }
    load()
    source.addEventListener('open', load)
    const remove = onEvent<LiveEvents, 'inbox'>(source, 'inbox', (item) =>
      setItems((list) => {
        const others = list.filter((other) => other.id !== item.id)
        return item.dismissedAt ? others : [...others, item]
      }),
    )
    return () => {
      source.removeEventListener('open', load)
      remove()
    }
  }, [source])

  return items
}
