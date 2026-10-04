import { useEffect, useState } from 'react'
import {
  listGitHubApps,
  listOrganizations,
  type GitHubApp,
} from '@/api/api.gen'
import { Badge } from '@/components/ui/badge'
import { LoginContext } from '@/lib/login'
import { Agents } from './Agents'
import { Checkup } from './Checkup'
import { Devices } from './Devices'
import { Frame } from './Frame'
import { GitHub } from './GitHub'
import { Login } from './Login'
import { Settings } from './Settings'
import { Workstreams } from './Workstreams'

const paths = [
  '/workstreams',
  '/agents',
  '/settings',
  '/settings/checkup',
  '/devices',
  '/github',
]

function currentPath() {
  if (!paths.includes(window.location.pathname)) {
    window.history.replaceState(null, '', '/workstreams')
  }
  return window.location.pathname
}

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
  const [path] = useState(currentPath)
  const [loginShown, setLoginShown] = useState(false)
  const [apps, setApps] = useState<GitHubApp[]>()
  const [organizations, setOrganizations] = useState<string[]>([])
  const [organization, setOrganization] = useState('')
  const [error, setError] = useState<string>()
  const [source, setSource] = useState<EventSource>()
  const live = !loginShown && apps !== undefined && apps.length > 0

  useEffect(() => {
    if (!live) {
      return
    }
    const events = new EventSource('/api/events')
    events.addEventListener('open', () => setSource(events), { once: true })
    return () => events.close()
  }, [live])

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
  return (
    <LoginContext value={() => setLoginShown(true)}>
      <Frame
        path={path}
        organizations={organizations}
        organization={organization}
        onSelect={selectOrganization}
        source={source}
      >
        {path === '/workstreams' && (
          <Workstreams organization={organization} source={source} />
        )}
        {path === '/agents' && <Agents source={source} />}
        {path === '/settings' && <Settings />}
        {path === '/settings/checkup' && (
          <Checkup organization={organization} />
        )}
        {path === '/devices' && <Devices />}
        {path === '/github' && <GitHub apps={apps} />}
      </Frame>
    </LoginContext>
  )
}

export default App
