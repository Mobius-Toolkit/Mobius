import { useCallback, useEffect, useState } from 'react'
import {
  listGitHubApps,
  listOrganizations,
  type Activity as ActivityRow,
  type GitHubApp,
  type LiveEvents,
} from '@/api/api.gen'
import { Badge } from '@/components/ui/badge'
import { onEvent } from '@/lib/events'
import { useInbox } from '@/lib/inbox'
import { LoginContext } from '@/lib/login'
import { unreadCount, useUnread } from '@/lib/unread'
import { useWorkstreams } from '@/lib/workstreams'
import { Activity } from './Activity'
import { Agents } from './Agents'
import { Chat } from './Chat'
import { Checkup } from './Checkup'
import { Devices } from './Devices'
import { Frame } from './Frame'
import { GitHub } from './GitHub'
import { Inbox } from './Inbox'
import { Login } from './Login'
import { NewWorkstream } from './NewWorkstream'
import { Settings } from './Settings'
import { Workstreams } from './Workstreams'

const paths = [
  '/workstreams',
  '/workstreams/new',
  '/inbox',
  '/activity',
  '/agents',
  '/settings',
  '/settings/checkup',
  '/devices',
  '/github',
]

const chatPattern = /^\/workstreams\/([^/]+)\/([^/]+)\/(\d+)$/

function currentPath() {
  const path = window.location.pathname
  if (!paths.includes(path) && !chatPattern.test(path)) {
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
  const [activities, setActivities] = useState<ActivityRow[]>([])
  const live = !loginShown && apps !== undefined && apps.length > 0
  const showLogin = useCallback(() => setLoginShown(true), [])
  const workstreams = useWorkstreams(showLogin, source)
  const unread = useUnread(source)
  const inbox = useInbox(source)
  const chat = chatPattern.exec(path)

  useEffect(() => {
    if (!live) {
      return
    }
    const connect = () => {
      const events = new EventSource('/api/events')
      events.addEventListener('open', () => setSource(events), { once: true })
      // The server sends the latest activities when the connection opens, before the pages can listen. When the
      // browser connects again, the server sends only the activities after the last event id.
      onEvent<LiveEvents, 'activity'>(events, 'activity', (activity) =>
        setActivities((list) =>
          list.some((other) => other.id === activity.id)
            ? list
            : [...list, activity],
        ),
      )
      return events
    }
    let events = connect()
    // A phone that stops the page in the background can leave a live connection open with no error. Thus the page
    // connects again when it is visible again or when the browser is online again.
    const wake = () => {
      if (!document.hidden) {
        events.close()
        events = connect()
      }
    }
    window.addEventListener('online', wake)
    document.addEventListener('visibilitychange', wake)
    return () => {
      window.removeEventListener('online', wake)
      document.removeEventListener('visibilitychange', wake)
      events.close()
    }
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
          const owner = chatPattern.exec(window.location.pathname)?.[1] ?? ''
          if (list.includes(owner)) {
            saveOrganization(owner)
          }
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
      <LoginContext value={showLogin}>
        <main className="flex min-h-svh items-center justify-center p-6">
          <GitHub apps={apps} />
        </main>
      </LoginContext>
    )
  }
  return (
    <LoginContext value={showLogin}>
      <Frame
        path={path}
        organizations={organizations}
        organization={organization}
        onSelect={selectOrganization}
        source={source}
        workstreams={workstreams}
        unread={unread ?? []}
        inbox={inbox}
        fill={chat !== null || path === '/workstreams/new'}
      >
        {chat && (
          <Chat
            owner={chat[1]}
            name={chat[2]}
            number={Number(chat[3])}
            workstreams={workstreams}
            unread={
              unread &&
              unreadCount(unread, {
                organization: chat[1],
                repository: `${chat[1]}/${chat[2]}`,
                workstream: Number(chat[3]),
              })
            }
            source={source}
          />
        )}
        {path === '/workstreams' && (
          <Workstreams
            organization={organization}
            workstreams={workstreams}
            unread={unread ?? []}
          />
        )}
        {path === '/workstreams/new' && (
          <NewWorkstream
            organizations={organizations}
            organization={organization}
            unread={
              unread &&
              unreadCount(unread, {
                organization,
                repository: '',
                workstream: 0,
              })
            }
            source={source}
          />
        )}
        {path === '/inbox' && (
          <Inbox
            organization={organization}
            items={inbox}
            workstreams={workstreams.list}
          />
        )}
        {path === '/activity' && (
          <Activity
            organization={organization}
            activities={activities}
            workstreams={workstreams.list}
          />
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
