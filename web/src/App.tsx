import { use, useEffect, useState } from 'react'
import {
  listWorkstreams,
  type Activity,
  type LiveEvents as LiveEvent,
  type Workstream,
} from '@/api/api.gen'
import { Badge } from '@/components/ui/badge'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { onEvent } from '@/lib/events'
import { LoginContext } from '@/lib/login'
import { Devices } from './Devices'
import { Login } from './Login'

const maxActivities = 50

function Workstreams() {
  const showLogin = use(LoginContext)
  const [workstreams, setWorkstreams] = useState<Workstream[]>()
  const [error, setError] = useState<string>()

  useEffect(() => {
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

  return (
    <Card>
      <CardHeader>
        <CardTitle>Workstreams</CardTitle>
        <CardDescription>The most recently active first</CardDescription>
      </CardHeader>
      <CardContent>
        {error && <Badge variant="destructive">{error}</Badge>}
        {!error && !workstreams && (
          <p className="text-muted-foreground">Loading</p>
        )}
        {workstreams?.length === 0 && (
          <p className="text-muted-foreground">No Workstreams</p>
        )}
        <ul className="divide-y">
          {workstreams?.map((ws) => (
            <li
              key={`${ws.repository}#${ws.number}`}
              className="flex items-center justify-between gap-4 py-2"
            >
              <span>
                {ws.repository} #{ws.number}
              </span>
              <span className="flex items-center gap-2 text-muted-foreground">
                <Badge variant="secondary">
                  {ws.openTasks} open / {ws.tasks} tasks
                </Badge>
                {new Date(ws.lastActivity).toLocaleString()}
              </span>
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  )
}

function LiveEvents() {
  const [activities, setActivities] = useState<Activity[]>([])
  const [connected, setConnected] = useState(false)

  useEffect(() => {
    const source = new EventSource('/api/events')
    source.addEventListener('open', () => setConnected(true))
    source.addEventListener('error', () => setConnected(false))
    onEvent<LiveEvent, 'activity'>(source, 'activity', (activity) =>
      setActivities((prev) => [activity, ...prev].slice(0, maxActivities)),
    )
    return () => source.close()
  }, [])

  return (
    <Card>
      <CardHeader>
        <CardTitle>Live events</CardTitle>
        <CardDescription>
          <Badge variant={connected ? 'default' : 'destructive'}>
            {connected ? 'Connected' : 'Not connected'}
          </Badge>
        </CardDescription>
      </CardHeader>
      <CardContent>
        <ul className="divide-y">
          {activities.map((a) => (
            <li key={a.id} className="grid gap-1 py-2">
              <a href={a.link} className="hover:underline">
                {a.text}
              </a>
              <span className="text-muted-foreground">
                {a.repository} #{a.workstream} · {a.actor} ·{' '}
                {new Date(a.time).toLocaleString()}
              </span>
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  )
}

const pages = [
  { path: '/', title: 'Workstreams' },
  { path: '/devices', title: 'Devices' },
]

function App() {
  const [loginShown, setLoginShown] = useState(false)

  if (loginShown) {
    return <Login onLogin={() => setLoginShown(false)} />
  }
  const path = window.location.pathname
  return (
    <LoginContext value={() => setLoginShown(true)}>
      <div className="mx-auto grid max-w-5xl gap-6 p-6">
        <nav className="flex gap-4">
          {pages.map((page) => (
            <a
              key={page.path}
              href={page.path}
              className={
                page.path === path
                  ? 'font-medium'
                  : 'text-muted-foreground hover:underline'
              }
            >
              {page.title}
            </a>
          ))}
        </nav>
        {path === '/devices' ? (
          <Devices />
        ) : (
          <main className="grid items-start gap-6 md:grid-cols-2">
            <Workstreams />
            <LiveEvents />
          </main>
        )}
      </div>
    </LoginContext>
  )
}

export default App
