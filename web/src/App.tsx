import { use, useEffect, useState } from 'react'
import {
  listGitHubApps,
  listOrganizations,
  listWorkstreams,
  type Activity,
  type GitHubApp,
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
import { Checkup } from './Checkup'
import { Devices } from './Devices'
import { GitHub } from './GitHub'
import { Login } from './Login'
import { OrganizationSwitch } from './OrganizationSwitch'

const maxActivities = 50

function Workstreams({ organization }: { organization: string }) {
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

  const shown = workstreams?.filter(
    (ws) => ws.repository.split('/')[0] === organization,
  )
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
        {shown?.length === 0 && (
          <p className="text-muted-foreground">No Workstreams</p>
        )}
        <ul className="divide-y">
          {shown?.map((ws) => (
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
  { path: '/github', title: 'GitHub' },
  { path: '/settings/checkup', title: 'Checkup' },
]

function savedOrganization() {
  try {
    return localStorage.getItem('organization') ?? ''
  } catch {
    return ''
  }
}

function saveOrganization(organization: string) {
  try {
    localStorage.setItem('organization', organization)
  } catch {
    // The page works with no saved organization.
  }
}

function App() {
  const [loginShown, setLoginShown] = useState(false)
  const [apps, setApps] = useState<GitHubApp[]>()
  const [organizations, setOrganizations] = useState<string[]>([])
  const [organization, setOrganization] = useState('')
  const [error, setError] = useState<string>()

  useEffect(() => {
    if (loginShown) {
      return
    }
    listGitHubApps()
      .then((res) => {
        if (res.status === 401) {
          setLoginShown(true)
        } else if (res.status === 200) {
          setApps(res.data.data)
        } else {
          setError(res.data.error)
        }
      })
      .catch((err: unknown) => setError(String(err)))
    listOrganizations()
      .then((res) => {
        if (res.status === 200) {
          const list = res.data.data
          const saved = savedOrganization()
          setOrganizations(list)
          setOrganization(list.includes(saved) ? saved : (list[0] ?? ''))
        }
      })
      .catch((err: unknown) => setError(String(err)))
  }, [loginShown])

  const selectOrganization = (name: string) => {
    saveOrganization(name)
    setOrganization(name)
  }

  if (loginShown) {
    return <Login onLogin={() => setLoginShown(false)} />
  }
  if (error) {
    return (
      <main className="p-6">
        <Badge variant="destructive">{error}</Badge>
      </main>
    )
  }
  if (!apps) {
    return null
  }
  if (apps.length === 0) {
    return (
      <LoginContext value={() => setLoginShown(true)}>
        <main className="flex min-h-svh items-center justify-center p-6">
          <GitHub apps={apps} />
        </main>
      </LoginContext>
    )
  }
  const path = window.location.pathname
  return (
    <LoginContext value={() => setLoginShown(true)}>
      <div className="mx-auto grid max-w-5xl gap-6 p-6">
        <nav className="flex items-center gap-4">
          {organizations.length > 1 ? (
            <OrganizationSwitch
              organizations={organizations}
              organization={organization}
              onSelect={selectOrganization}
            />
          ) : (
            <span className="font-semibold">Mobius</span>
          )}
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
        {path === '/devices' && <Devices />}
        {path === '/github' && <GitHub apps={apps} />}
        {path === '/settings/checkup' && (
          <Checkup organization={organization} />
        )}
        {path !== '/devices' &&
          path !== '/github' &&
          path !== '/settings/checkup' && (
            <main className="grid items-start gap-6 md:grid-cols-2">
              <Workstreams organization={organization} />
              <LiveEvents />
            </main>
          )}
      </div>
    </LoginContext>
  )
}

export default App
