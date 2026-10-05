import { useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import {
  completeWorkstream,
  resumeIssue,
  setAutopilot,
  type NeedsHuman,
} from '@/api/api.gen'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import {
  Sheet,
  SheetContent,
  SheetTitle,
  SheetTrigger,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'
import type { Workstreams } from '@/lib/workstreams'
import { AgentPanel } from './AgentPanel'
import { Conversation } from './Conversation'

function NeedsHumanList({
  issues,
  onChange,
}: {
  issues: NeedsHuman[]
  onChange: () => void
}) {
  const [error, setError] = useState('')
  if (issues.length === 0) {
    return null
  }
  const resume = (issue: NeedsHuman) => {
    const [owner, name] = issue.repository.split('/')
    resumeIssue(owner, name, issue.number)
      .then((res) => setError(res.status === 204 ? '' : res.data.error))
      .catch((err: unknown) => setError(String(err)))
      .finally(onChange)
  }
  return (
    <div className="grid gap-1 border-t px-4 py-2">
      {error && (
        <Badge
          variant="destructive"
          className="h-auto w-full justify-start whitespace-normal"
        >
          {error}
        </Badge>
      )}
      {issues.map((issue) => (
        <div key={issue.number} className="flex items-center gap-3">
          <a
            href={issue.url}
            target="_blank"
            rel="noreferrer"
            className="grow text-primary underline-offset-4 hover:underline"
          >
            #{issue.number} {issue.title}
          </a>
          {issue.pullRequestUrl && (
            <a
              href={issue.pullRequestUrl}
              target="_blank"
              rel="noreferrer"
              className="shrink-0 text-primary underline-offset-4 hover:underline"
            >
              PR #{issue.pullRequest}
            </a>
          )}
          <Button size="sm" onClick={() => resume(issue)}>
            Resume
          </Button>
        </div>
      ))}
    </div>
  )
}

export function Chat({
  owner,
  name,
  number,
  workstreams,
  unread,
  source,
}: {
  owner: string
  name: string
  number: number
  workstreams: Workstreams
  unread?: number
  source?: EventSource
}) {
  const repository = `${owner}/${name}`
  const workstream = workstreams.list?.find(
    (other) => other.repository === repository && other.number === number,
  )
  const [autopilotBusy, setAutopilotBusy] = useState(false)
  const [autopilotError, setAutopilotError] = useState('')
  const [closeBusy, setCloseBusy] = useState(false)
  const [closeError, setCloseError] = useState('')
  const navigate = useNavigate()

  const switchAutopilot = (on: boolean) => {
    setAutopilotBusy(true)
    setAutopilot(owner, name, number, { on })
      .then((res) =>
        setAutopilotError(res.status === 204 ? '' : res.data.error),
      )
      .catch((err: unknown) => setAutopilotError(String(err)))
      .finally(() => {
        setAutopilotBusy(false)
        workstreams.load()
      })
  }

  const close = () => {
    setCloseBusy(true)
    setCloseError('')
    completeWorkstream(owner, name, number)
      .then((res) => {
        if (res.status === 204) {
          void navigate({ to: '/workstreams' })
        } else {
          setCloseError(res.data.error)
          setCloseBusy(false)
        }
      })
      .catch((err: unknown) => {
        setCloseError(String(err))
        setCloseBusy(false)
      })
  }

  const panel = (
    <AgentPanel owner={owner} name={name} number={number} source={source} />
  )
  return (
    <div className="flex min-h-0 min-w-0 grow">
      <Conversation
        organization={owner}
        repository={repository}
        workstream={number}
        agent="Lead"
        source={source}
        unread={unread}
        brief={workstream}
        head={
          <>
            <h2 className="min-w-0 truncate font-semibold">
              {workstream?.title}
            </h2>
            <span className="text-sm text-muted-foreground">#{number}</span>
            <Switch
              id="autopilot"
              checked={workstream?.autopilot ?? false}
              disabled={!workstream || autopilotBusy}
              onCheckedChange={switchAutopilot}
            />
            <Label htmlFor="autopilot">Autopilot</Label>
          </>
        }
        tail={
          <Sheet>
            <SheetTrigger asChild>
              <Button variant="outline" size="sm" className="md:hidden">
                Agents
              </Button>
            </SheetTrigger>
            <SheetContent
              side="bottom"
              aria-describedby={undefined}
              className="pt-2 data-[side=bottom]:h-[80svh]"
            >
              <SheetTitle className="sr-only">Agents and tasks</SheetTitle>
              {panel}
            </SheetContent>
          </Sheet>
        }
        note={
          <>
            {autopilotError && (
              <p className="border-b px-4 py-2 text-sm text-destructive">
                {autopilotError}
              </p>
            )}
            {workstream?.allTasksClosed && (
              <div className="flex flex-wrap items-center gap-3 border-b px-4 py-2">
                <span>All tasks are closed.</span>
                <Button size="sm" disabled={closeBusy} onClick={close}>
                  Close Workstream
                </Button>
                {closeError && (
                  <span className="text-sm text-destructive">{closeError}</span>
                )}
              </div>
            )}
          </>
        }
        footer={
          <NeedsHumanList
            issues={workstreams.needsHuman.filter(
              (issue) =>
                issue.repository === repository && issue.workstream === number,
            )}
            onChange={workstreams.load}
          />
        }
      />
      <aside className="hidden w-80 shrink-0 flex-col border-l pt-2 md:flex">
        {panel}
      </aside>
    </div>
  )
}
