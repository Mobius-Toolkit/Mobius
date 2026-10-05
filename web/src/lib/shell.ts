import { createContext, useContext } from 'react'
import type {
  Activity,
  GitHubApp,
  InboxItem,
  Unread,
} from '@/api/api.gen'
import type { Workstreams } from './workstreams'

type Shell = {
  apps: GitHubApp[]
  organizations: string[]
  organization: string
  source?: EventSource
  workstreams: Workstreams
  unread?: Unread[]
  inbox: InboxItem[]
  activities: Activity[]
}

export const ShellContext = createContext<Shell | undefined>(undefined)

export function useShell() {
  const shell = useContext(ShellContext)
  if (!shell) {
    throw new Error('The page is not in the shell.')
  }
  return shell
}
