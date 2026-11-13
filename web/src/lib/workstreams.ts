import { Effect, Schema } from "effect";
import {
  listActiveAgents,
  listNeedsHuman,
  listWorkstreams,
  type NeedsHuman,
  type Workstream,
} from "@/api/api.gen";
import { Agent } from "@/transcript/schemas";
import { request } from "./effects";
import { onEvent } from "./events";
import { dropMarks } from "./inbox";

const AnyRecord = Schema.Record(Schema.String, Schema.Json);
const WorkstreamRef = Schema.Struct({ repository: Schema.String, number: Schema.Number });

export type WorkstreamState = {
  list?: readonly Workstream[];
  needsHuman: readonly NeedsHuman[];
  working: Set<string>;
  error?: string;
};

let state: WorkstreamState = { needsHuman: [], working: new Set() };
let showLogin = () => {};
const listeners = new Set<() => void>();

export function currentWorkstreams() {
  return state;
}

export function subscribeWorkstreams(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

function setState(change: Partial<WorkstreamState>) {
  state = { ...state, ...change };
  listeners.forEach((listener) => listener());
}

export function workstreamKey(workstream: { repository: string; number: number }) {
  return `${workstream.repository}#${workstream.number}`;
}

export function chatParams(workstream: { repository: string; number: number }) {
  const [owner, name] = workstream.repository.split("/");
  return { owner, name, number: String(workstream.number) };
}

export function organizationWorkstreams(workstreams: WorkstreamState, organization: string) {
  return workstreams.list?.filter((workstream) =>
    workstream.repository.startsWith(`${organization}/`),
  );
}

const loadWorking = Effect.fnUntraced(function* () {
  const res = yield* request(() => listActiveAgents());
  if (res.status === 200) {
    setState({
      working: new Set(
        res.data.data.groups
          .flatMap((group) => group.agents)
          .filter((row) => row.agent.working)
          .map((row) =>
            workstreamKey({
              repository: row.agent.workstreamRepository,
              number: row.agent.workstream,
            }),
          ),
      ),
    });
  }
}, Effect.ignore);

const loadList = request(() => listWorkstreams()).pipe(
  Effect.map((res) => {
    if (res.status === 401) {
      showLogin();
    } else if (res.status === 200) {
      setState({ list: res.data.data });
    } else {
      setState({ error: res.data.error });
    }
  }),
  Effect.catchTag("RequestFailed", (error) =>
    Effect.sync(() => setState({ error: error.message })),
  ),
);

const loadNeedsHuman = Effect.fnUntraced(function* () {
  const res = yield* request(() => listNeedsHuman());
  if (res.status === 200) {
    setState({ needsHuman: res.data.data });
    dropMarks(res.data.data);
  }
}, Effect.ignore);

export const loadWorkstreams = () =>
  Effect.all([loadList, loadNeedsHuman(), loadWorking()], { concurrency: "unbounded" }).pipe(
    Effect.asVoid,
  );

const reloadWorkstreams = () => {
  Effect.runFork(loadWorkstreams());
};

const reloadWorking = () => {
  Effect.runFork(loadWorking());
};

export function startWorkstreams(source: EventSource, onLogin: () => void) {
  showLogin = onLogin;
  // An event of the lists that comes while the connection is down is lost, so each connection reads the lists.
  reloadWorkstreams();
  source.addEventListener("open", reloadWorkstreams);
  const removeChange = onEvent(source, "workstreams", AnyRecord, reloadWorkstreams);
  const removeCreated = onEvent(source, "workstreamCreated", WorkstreamRef, reloadWorkstreams);
  const removeAgent = onEvent(source, "agent", Agent, reloadWorking);
  return () => {
    source.removeEventListener("open", reloadWorkstreams);
    removeChange();
    removeCreated();
    removeAgent();
  };
}
