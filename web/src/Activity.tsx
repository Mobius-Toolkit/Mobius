import { useState } from 'react'
import type { Activity as ActivityRow, Workstream } from '@/api/api.gen'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { dayClock } from '@/lib/time'

export function Activity({
  organization,
  activities,
  workstreams,
}: {
  organization: string
  activities: ActivityRow[]
  workstreams?: Workstream[]
}) {
  // selected is the key of the shown Workstream, or empty for all.
  const [selected, setSelected] = useState('')
  const inOrganization = (repository: string) =>
    repository.startsWith(`${organization}/`)
  const rows = activities
    .filter(
      (row) =>
        inOrganization(row.repository) &&
        (!selected || `${row.repository}#${row.workstream}` === selected),
    )
    .toSorted((a, b) => b.id - a.id)
  return (
    <Card>
      <CardHeader>
        <CardTitle>Activity</CardTitle>
      </CardHeader>
      <CardContent className="grid gap-4">
        <div className="flex flex-wrap gap-2">
          <Button
            size="sm"
            variant={selected ? 'outline' : 'secondary'}
            aria-pressed={!selected}
            onClick={() => setSelected('')}
          >
            All
          </Button>
          {workstreams
            ?.filter((workstream) => inOrganization(workstream.repository))
            .map((workstream) => {
              const key = `${workstream.repository}#${workstream.number}`
              return (
                <Button
                  key={key}
                  size="sm"
                  variant={selected === key ? 'secondary' : 'outline'}
                  aria-pressed={selected === key}
                  onClick={() => setSelected(key)}
                >
                  {workstream.title}
                </Button>
              )
            })}
        </div>
        {rows.length === 0 && (
          <p className="text-muted-foreground">No activity.</p>
        )}
        <ul className="divide-y">
          {rows.map((row) => (
            <li key={row.id} className="flex items-baseline gap-3 py-2">
              <span className="shrink-0 text-sm text-muted-foreground">
                {dayClock(row.time)}
              </span>
              <span className="min-w-0 grow break-words">
                @{row.actor} {row.text}
              </span>
              <a
                href={row.link}
                target="_blank"
                rel="noreferrer"
                className="text-primary underline-offset-4 hover:underline"
              >
                #{row.issue}
              </a>
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  )
}
