import {
  listActiveAgents,
  listNeedsHuman,
  listWorkstreams,
  type LiveEvents,
  type NeedsHuman,
  type Workstream,
} from "@/api/api.gen";
import { onEvent } from "./events";

export type WorkstreamState = {
  list?: Workstream[];
  needsHuman: NeedsHuman[];
  working: Set<string>;
  error?: string;
};

export type Workstreams = WorkstreamState & { load: () => Promise<unknown> };

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

export function organizationWorkstreams(workstreams: Workstreams, organization: string) {
  return workstreams.list?.filter((workstream) =>
    workstream.repository.startsWith(`${organization}/`),
  );
}

function loadWorking() {
  return listActiveAgents()
    .then((res) => {
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
    })
    .catch(() => {});
}

export function loadWorkstreams() {
  const lists = listWorkstreams()
    .then((res) => {
      if (res.status === 401) {
        showLogin();
      } else if (res.status === 200) {
        setState({ list: res.data.data });
      } else {
        setState({ error: res.data.error });
      }
    })
    .catch((err: unknown) => setState({ error: String(err) }));
  const needsHumanList = listNeedsHuman()
    .then((res) => {
      if (res.status === 200) {
        setState({ needsHuman: res.data.data });
      }
    })
    .catch(() => {});
  return Promise.all([lists, needsHumanList, loadWorking()]);
}

export function startWorkstreams(source: EventSource, onLogin: () => void) {
  showLogin = onLogin;
  // An event of the lists that comes while the connection is down is lost, so each connection reads the lists.
  loadWorkstreams();
  source.addEventListener("open", loadWorkstreams);
  const removeChange = onEvent<LiveEvents, "workstreams">(source, "workstreams", loadWorkstreams);
  const removeCreated = onEvent<LiveEvents, "workstreamCreated">(
    source,
    "workstreamCreated",
    loadWorkstreams,
  );
  const removeAgent = onEvent<LiveEvents, "agent">(source, "agent", loadWorking);
  return () => {
    source.removeEventListener("open", loadWorkstreams);
    removeChange();
    removeCreated();
    removeAgent();
  };
}
