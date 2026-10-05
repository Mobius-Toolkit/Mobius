import { ChevronLeftIcon } from 'lucide-react'
import { use, useCallback, useEffect, useState } from 'react'
import {
  getTranscript,
  listActiveAgents,
  type ActiveAgent,
  type ActiveAgents,
  type Agent,
  type LiveEvents,
  type TranscriptLine,
} from '@/api/api.gen'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardAction,
  CardContent,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { onEvent } from '@/lib/events'
import { LoginContext } from '@/lib/login'
import { BackButton } from './BackButton'
import { cn } from '@/lib/utils'

// The queue reason of a session that waits for the end of a usage-limit pause starts with this text.
export const paused = 'paused until '

function numbered(number: number, title?: string | null) {
  return title ? `#${number} ${title}` : `#${number}`
}

function AgentRow({
  row,
  onOpen,
}: {
  row: ActiveAgent
  onOpen: (agent: Agent) => void
}) {
  const agent = row.agent
  return (
    <li>
      <button
        type="button"
        onClick={() => onOpen(agent)}
        className="flex w-full items-center gap-3 rounded-lg px-2 py-2 text-left hover:bg-muted"
      >
        <span
          className={cn(
            'size-2 shrink-0 rounded-full',
            agent.queueReason ? 'bg-amber-500' : 'bg-green-600',
          )}
        />
        <span className="grid min-w-0 grow gap-0.5">
          <span>
            {agent.name} {agent.title}
          </span>
          <span className="text-sm text-muted-foreground">
            {[
              agent.role,
              agent.organization,
              (agent.workstream !== 0 || agent.issue != null) &&
                agent.repository,
              agent.queueReason,
            ]
              .filter(Boolean)
              .join(' · ')}
          </span>
          {agent.workstream !== 0 && (
            <span className="truncate text-sm text-muted-foreground">
              Workstream {numbered(agent.workstream, row.workstreamTitle)}
            </span>
          )}
          {agent.issue != null && (
            <span className="truncate text-sm text-muted-foreground">
              Ticket {numbered(agent.issue, row.issueTitle)}
            </span>
          )}
          {row.pullRequest != null && (
            <span className="text-sm text-muted-foreground">
              Pull request #{row.pullRequest}
            </span>
          )}
        </span>
        {agent.queueReason && (
          <Badge variant="outline">
            {agent.queueReason.startsWith(paused) ? 'paused' : 'queued'}
          </Badge>
        )}
      </button>
    </li>
  )
}

function TranscriptEntry({ line }: { line: TranscriptLine }) {
  const [open, setOpen] = useState(!line.folded)
  const [raw, setRaw] = useState(false)
  return (
    <li
      className={cn(
        'grid grid-cols-[auto_auto_minmax(0,1fr)] gap-x-3 py-2',
        line.error && 'text-destructive',
      )}
    >
      <span className="text-muted-foreground tabular-nums">
        {new Date(line.time).toLocaleTimeString([], {
          hour: '2-digit',
          minute: '2-digit',
        })}
      </span>
      <span className="font-mono text-xs leading-5">{line.kind}</span>
      <div className="grid gap-1">
        <span className="break-words">
          {line.text}
          {line.harnessToolName && (
            <span className="text-sm text-muted-foreground">
              {' '}
              {line.harnessToolName}
            </span>
          )}
        </span>
        <span className="flex gap-1">
          {line.folded && line.body && (
            <Button variant="ghost" size="xs" onClick={() => setOpen(!open)}>
              {open ? 'Hide' : 'Show'}
            </Button>
          )}
          <Button variant="ghost" size="xs" onClick={() => setRaw(!raw)}>
            Raw
          </Button>
        </span>
        {open && line.body && (
          <pre className="overflow-x-auto rounded-md bg-muted p-2 text-xs whitespace-pre-wrap">
            {line.body}
          </pre>
        )}
        {raw && (
          <pre className="overflow-x-auto rounded-md bg-muted p-2 text-xs">
            {line.raw}
          </pre>
        )}
      </div>
    </li>
  )
}

export function Transcript({
  agent,
  onClose,
}: {
  agent: Agent
  onClose: () => void
}) {
  const showLogin = use(LoginContext)
  const [lines, setLines] = useState<TranscriptLine[]>()
  const [error, setError] = useState<string>()

  useEffect(() => {
    getTranscript(agent.id)
      .then((res) => {
        if (res.status === 401) {
          showLogin()
        } else if (res.status === 200) {
          setLines(res.data.data)
        } else {
          setError(res.data.error)
        }
      })
      .catch((err: unknown) => setError(String(err)))
  }, [agent.id, showLogin])

  return (
    <Card>
      <CardHeader>
        <CardTitle className="truncate">
          {agent.name} {agent.title}
        </CardTitle>
        <CardAction>
          <Button variant="ghost" onClick={onClose}>
            <ChevronLeftIcon />
            Agents
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className="grid gap-4">
        {error && <Badge variant="destructive">{error}</Badge>}
        <ul className="divide-y">
          {lines?.map((line) => (
            <TranscriptEntry key={line.id} line={line} />
          ))}
        </ul>
        <p className="text-sm text-muted-foreground">
          Read only. The Owner talks only to the Lead.
        </p>
      </CardContent>
    </Card>
  )
}

export function Agents({ source }: { source?: EventSource }) {
  const showLogin = use(LoginContext)
  const [agents, setAgents] = useState<ActiveAgents>()
  const [error, setError] = useState<string>()
  const [selected, setSelected] = useState<Agent>()

  const load = useCallback(() => {
    listActiveAgents()
      .then((res) => {
        if (res.status === 401) {
          showLogin()
        } else if (res.status === 200) {
          setAgents(res.data.data)
        } else {
          setError(res.data.error)
        }
      })
      .catch((err: unknown) => setError(String(err)))
  }, [showLogin])

  useEffect(load, [load])

  // An agent event that comes while the connection is down is lost, so each connection reads the list.
  useEffect(() => {
    if (!source) {
      return
    }
    source.addEventListener('open', load)
    const remove = onEvent<LiveEvents, 'agent'>(source, 'agent', load)
    return () => {
      source.removeEventListener('open', load)
      remove()
    }
  }, [source, load])

  if (selected) {
    return (
      <Transcript agent={selected} onClose={() => setSelected(undefined)} />
    )
  }
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <BackButton parent="/settings" />
          Agents
        </CardTitle>
        {agents && (
          <CardAction className="text-muted-foreground">
            {agents.count} / {agents.max}
          </CardAction>
        )}
      </CardHeader>
      <CardContent className="grid gap-4">
        {error && <Badge variant="destructive">{error}</Badge>}
        {agents?.groups.map((group) => (
          <section key={group.name} className="grid gap-1">
            <h3 className="text-xs font-semibold tracking-wide text-muted-foreground uppercase">
              {group.name} {group.count} / {group.max}
            </h3>
            <ul>
              {group.agents.map((row) => (
                <AgentRow key={row.agent.id} row={row} onOpen={setSelected} />
              ))}
            </ul>
          </section>
        ))}
      </CardContent>
    </Card>
  )
}
