import { use, useCallback, useEffect, useState } from 'react'
import {
  listWorkstreams,
  type LiveEvents,
  type Workstream,
} from '@/api/api.gen'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { onEvent } from '@/lib/events'
import { LoginContext } from '@/lib/login'

export function Workstreams({
  organization,
  source,
}: {
  organization: string
  source?: EventSource
}) {
  const showLogin = use(LoginContext)
  const [workstreams, setWorkstreams] = useState<Workstream[]>()
  const [error, setError] = useState<string>()

  const load = useCallback(() => {
    listWorkstreams()
      .then((res) => {
        if (res.status === 401) {
          showLogin()
        } else if (res.status === 200) {
          setWorkstreams(res.data.data)
        } else {
          setError(res.data.error)
        }
      })
      .catch((err: unknown) => setError(String(err)))
  }, [showLogin])

  useEffect(load, [load])

  // An event of the list that comes while the connection is down is lost, so each connection reads the list.
  useEffect(() => {
    if (!source) {
      return
    }
    source.addEventListener('open', load)
    const remove = onEvent<LiveEvents, 'workstreams'>(
      source,
      'workstreams',
      load,
    )
    return () => {
      source.removeEventListener('open', load)
      remove()
    }
  }, [source, load])

  const shown = workstreams?.filter((workstream) =>
    workstream.repository.startsWith(`${organization}/`),
  )
  return (
    <Card>
      <CardHeader>
        <CardTitle>Workstreams</CardTitle>
      </CardHeader>
      <CardContent>
        {error && <Badge variant="destructive">{error}</Badge>}
        {shown?.length === 0 && (
          <p className="text-muted-foreground">
            This organization has no open Workstream.
          </p>
        )}
        <ul className="divide-y">
          {shown?.map((workstream) => (
            <li
              key={`${workstream.repository}#${workstream.number}`}
              className="flex items-center justify-between gap-4 py-2"
            >
              <span>{workstream.title}</span>
              <span className="flex items-center gap-2">
                <span className="text-muted-foreground">
                  #{workstream.number}
                </span>
                {workstream.allTasksClosed && (
                  <Badge variant="secondary">done</Badge>
                )}
              </span>
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  )
}
