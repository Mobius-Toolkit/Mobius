import { useCallback, useEffect, useState } from "react";
import {
  listActiveAgents,
  listNeedsHuman,
  listWorkstreams,
  type LiveEvents,
  type NeedsHuman,
  type Workstream,
} from "@/api/api.gen";
import { onEvent } from "./events";

export type Workstreams = ReturnType<typeof useWorkstreams>;

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

export function useWorkstreams(showLogin: () => void, source?: EventSource) {
  const [list, setList] = useState<Workstream[]>();
  const [needsHuman, setNeedsHuman] = useState<NeedsHuman[]>([]);
  const [working, setWorking] = useState<Set<string>>(new Set());
  const [error, setError] = useState<string>();

  const loadWorking = useCallback(
    () =>
      listActiveAgents()
        .then((res) => {
          if (res.status === 200) {
            setWorking(
              new Set(
                res.data.data.groups
                  .flatMap((group) => group.agents)
                  .filter((row) => row.agent.working)
                  .map((row) =>
                    workstreamKey({
                      repository: row.agent.repository,
                      number: row.agent.workstream,
                    }),
                  ),
              ),
            );
          }
        })
        .catch(() => {}),
    [],
  );

  const load = useCallback(() => {
    const lists = listWorkstreams()
      .then((res) => {
        if (res.status === 401) {
          showLogin();
        } else if (res.status === 200) {
          setList(res.data.data);
        } else {
          setError(res.data.error);
        }
      })
      .catch((err: unknown) => setError(String(err)));
    const needsHumanList = listNeedsHuman()
      .then((res) => {
        if (res.status === 200) {
          setNeedsHuman(res.data.data);
        }
      })
      .catch(() => {});
    return Promise.all([lists, needsHumanList, loadWorking()]);
  }, [showLogin, loadWorking]);

  // An event of the lists that comes while the connection is down is lost, so each connection reads the lists.
  useEffect(() => {
    if (!source) {
      return;
    }
    load();
    source.addEventListener("open", load);
    const removeChange = onEvent<LiveEvents, "workstreams">(source, "workstreams", load);
    const removeCreated = onEvent<LiveEvents, "workstreamCreated">(
      source,
      "workstreamCreated",
      load,
    );
    const removeAgent = onEvent<LiveEvents, "agent">(source, "agent", loadWorking);
    return () => {
      source.removeEventListener("open", load);
      removeChange();
      removeCreated();
      removeAgent();
    };
  }, [source, load, loadWorking]);

  return { list, needsHuman, working, error, load };
}
