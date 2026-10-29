import { Link } from "@tanstack/react-router";
import { useState } from "react";
import type { HeldWork, Switches } from "@/api/api.gen";
import { ErrorBadge } from "@/components/page";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { switchKinds, type ChangeSwitch, type SwitchKind } from "@/lib/switches";
import { chatParams } from "@/lib/workstreams";

function HeldList({ title, held }: { title: string; held: HeldWork[] }) {
  const [open, setOpen] = useState(false);
  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        <Button size="sm" variant="outline">
          {held.length} held
        </Button>
      </SheetTrigger>
      <SheetContent side="bottom" aria-describedby={undefined} className="gap-2 p-4 pt-3">
        <SheetTitle>{title}</SheetTitle>
        {held.length === 0 ? (
          <p className="text-muted-foreground">Nothing waits.</p>
        ) : (
          <ul className="max-h-[60svh] divide-y overflow-y-auto">
            {held.map((item) => (
              <li key={`${item.repository}#${item.issue}`}>
                <Link
                  to="/workstreams/$owner/$name/$number"
                  params={chatParams({
                    repository: item.workstreamRepository,
                    number: item.workstream,
                  })}
                  className="grid gap-0.5 rounded-md px-2 py-2 hover:bg-accent"
                  onClick={() => setOpen(false)}
                >
                  <span className="wrap-anywhere">
                    {item.repository === item.workstreamRepository
                      ? `#${item.issue}`
                      : `${item.repository}#${item.issue}`}{" "}
                    {item.title}
                  </span>
                  <span className="text-xs text-muted-foreground wrap-anywhere">
                    {item.workstreamTitle}
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        )}
      </SheetContent>
    </Sheet>
  );
}

function SwitchLine({
  kind,
  line,
  held,
  error,
  change,
}: {
  kind: SwitchKind;
  line: string;
  held: HeldWork[];
  error?: string;
  change: ChangeSwitch;
}) {
  const [running, setRunning] = useState(false);

  const resume = () => {
    setRunning(true);
    void change(kind, true).then(() => setRunning(false));
  };

  return (
    <div className="grid gap-2">
      <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-amber-500/40 bg-amber-100 px-3 py-2 text-sm text-amber-900 dark:bg-amber-950 dark:text-amber-200">
        <span>{line}</span>
        <span className="flex items-center gap-2">
          <HeldList title={line} held={held} />
          <Button size="sm" disabled={running} pending={running} onClick={resume}>
            Resume
          </Button>
        </span>
      </div>
      {error && <ErrorBadge>{error}</ErrorBadge>}
    </div>
  );
}

export function SwitchLines({
  switches,
  errors,
  change,
}: {
  switches?: Switches;
  errors: Partial<Record<SwitchKind, string>>;
  change: ChangeSwitch;
}) {
  if (!switches) {
    return null;
  }
  return switchKinds
    .filter(({ kind }) => !switches[kind])
    .map(({ kind, line }) => (
      <SwitchLine
        key={kind}
        kind={kind}
        line={line}
        held={switches.held.filter((item) => item.kind === kind)}
        error={errors[kind]}
        change={change}
      />
    ));
}
