import { useState } from "react";
import type { Activity as ActivityRow, Workstream } from "@/api/api.gen";
import { inset, List, Row } from "@/components/page";
import { Button } from "@/components/ui/button";
import { dayClock } from "@/lib/time";
import { cn } from "@/lib/utils";

export function Activity({
  organization,
  activities,
  workstreams,
}: {
  organization: string;
  activities: ActivityRow[];
  workstreams?: Workstream[];
}) {
  // selected is the key of the shown Workstream, or empty for all.
  const [selected, setSelected] = useState("");
  const inOrganization = (repository: string) => repository.startsWith(`${organization}/`);
  const rows = activities
    .filter(
      (row) =>
        inOrganization(row.repository) &&
        (!selected || `${row.repository}#${row.workstream}` === selected),
    )
    .toSorted((a, b) => b.id - a.id);
  return (
    <div className="grid gap-4">
      <div className={cn("flex flex-wrap gap-2", inset)}>
        <Button
          size="sm"
          variant={selected ? "outline" : "secondary"}
          aria-pressed={!selected}
          onClick={() => setSelected("")}
        >
          All
        </Button>
        {workstreams
          ?.filter((workstream) => inOrganization(workstream.repository))
          .map((workstream) => {
            const key = `${workstream.repository}#${workstream.number}`;
            return (
              <Button
                key={key}
                size="sm"
                variant={selected === key ? "secondary" : "outline"}
                aria-pressed={selected === key}
                onClick={() => setSelected(key)}
              >
                {workstream.title}
              </Button>
            );
          })}
      </div>
      {rows.length === 0 && <p className={cn("text-muted-foreground", inset)}>No activity.</p>}
      <List>
        {rows.map((row) => (
          <Row key={row.id} className="items-baseline gap-3">
            <span className="shrink-0 text-sm text-muted-foreground">{dayClock(row.time)}</span>
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
          </Row>
        ))}
      </List>
    </div>
  );
}
