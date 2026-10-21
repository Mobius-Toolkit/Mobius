import { Link } from "@tanstack/react-router";
import { use, useCallback, useEffect, useState } from "react";
import {
  listBoardTasks,
  type BoardTask,
  type BoardTaskState,
  type LiveEvents,
} from "@/api/api.gen";
import { ErrorBadge, PageHeader } from "@/components/page";
import { Badge } from "@/components/ui/badge";
import { onEvent } from "@/lib/events";
import { LoginContext } from "@/lib/login";
import { elapsed } from "@/lib/time";
import { chatParams } from "@/lib/workstreams";
import { link } from "./Inbox";
import { TopBar } from "./TopBar";

const columns: { title: string; states: BoardTaskState[] }[] = [
  { title: "Queued", states: ["dispatched", "queued", "answered"] },
  { title: "Working", states: ["working", "reviewed"] },
  { title: "Waiting", states: ["checks", "approval"] },
  { title: "Needs you", states: ["needs_human", "asked", "stopped"] },
  { title: "Ready to merge", states: ["ready_for_review"] },
  { title: "Done", states: ["ended"] },
];

function TaskCard({ task }: { task: BoardTask }) {
  const rounds = [
    task.fixRounds > 0 && `fix ${task.fixRounds}`,
    task.reviewRounds > 0 && `review ${task.reviewRounds}`,
  ].filter(Boolean);
  return (
    <li className="relative grid grid-cols-1 gap-1 rounded-lg border bg-card p-3 text-sm hover:bg-muted">
      <Link
        to="/workstreams/$owner/$name/$number"
        params={chatParams({ repository: task.workstreamRepository, number: task.workstream })}
        className="line-clamp-2 font-medium wrap-anywhere after:absolute after:inset-0"
      >
        {task.title}
      </Link>
      <span className="truncate text-muted-foreground">
        {task.repository.split("/")[1]}#{task.number} · {task.workstreamTitle}
      </span>
      <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <Badge variant="secondary">{task.state}</Badge>
        {task.pullRequest !== null && task.pullRequestUrl !== null && (
          <a
            href={task.pullRequestUrl}
            target="_blank"
            rel="noreferrer"
            className={`relative z-10 whitespace-nowrap ${link}`}
          >
            PR #{task.pullRequest}
          </a>
        )}
        <span className="ml-auto whitespace-nowrap text-muted-foreground">
          {elapsed(task.stateAt)}
        </span>
      </span>
      {rounds.length > 0 && <span className="text-muted-foreground">{rounds.join(" · ")}</span>}
    </li>
  );
}

export function Tasks({ organization, source }: { organization: string; source?: EventSource }) {
  const showLogin = use(LoginContext);
  const [tasks, setTasks] = useState<BoardTask[]>();
  const [error, setError] = useState("");

  const load = useCallback(() => {
    listBoardTasks()
      .then((res) => {
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 200) {
          setTasks(res.data.data);
          setError("");
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => setError(String(err)));
  }, [showLogin]);

  // An event that comes while the connection is down is lost, so each connection reads the tasks.
  useEffect(() => {
    if (!source) {
      return;
    }
    load();
    source.addEventListener("open", load);
    const remove = onEvent<LiveEvents, "workstreams">(source, "workstreams", load);
    return () => {
      source.removeEventListener("open", load);
      remove();
    };
  }, [source, load]);

  const shown = tasks?.filter((task) => task.workstreamRepository.startsWith(`${organization}/`));
  return (
    <div className="flex min-h-0 min-w-0 grow flex-col gap-2 pt-4">
      <TopBar title="Tasks" />
      <div className="px-4">
        <PageHeader title="Tasks" />
      </div>
      {error && <ErrorBadge>{error}</ErrorBadge>}
      {shown && (
        <div className="flex min-h-0 grow snap-x snap-mandatory scroll-px-4 gap-3 overflow-x-auto px-4 pb-4 md:snap-none">
          {columns.map(({ title, states }) => {
            const cards = shown
              .filter((task) => states.includes(task.state))
              .toSorted((a, b) =>
                title === "Done"
                  ? Date.parse(b.stateAt) - Date.parse(a.stateAt)
                  : Date.parse(a.stateAt) - Date.parse(b.stateAt),
              );
            return (
              <section
                key={title}
                aria-label={title}
                className="flex min-h-0 w-[85vw] shrink-0 snap-start flex-col gap-2 md:w-0 md:flex-1"
              >
                <h3 className="flex items-center gap-2 text-xs font-semibold tracking-wide text-muted-foreground uppercase">
                  {title}
                  <Badge variant="secondary">{cards.length}</Badge>
                </h3>
                {cards.length === 0 && <p className="text-sm text-muted-foreground">No tasks</p>}
                <ul className="grid min-h-0 grid-cols-1 content-start gap-2 overflow-y-auto">
                  {cards.map((task) => (
                    <TaskCard key={`${task.repository}#${task.number}`} task={task} />
                  ))}
                </ul>
              </section>
            );
          })}
        </div>
      )}
    </div>
  );
}
