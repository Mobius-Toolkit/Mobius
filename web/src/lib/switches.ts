import { useCallback, useEffect, useState } from "react";
import { getSwitches, setSwitch, type LiveEvents, type Switches } from "@/api/api.gen";
import { onEvent } from "./events";

export type SwitchKind = "start" | "merge" | "repairs";

export const switchKinds: {
  kind: SwitchKind;
  title: string;
  description: string;
  line: string;
}[] = [
  {
    kind: "start",
    title: "Start",
    description: "Mobius starts new tasks and new sessions of tasks.",
    line: "Autopilot paused: no task starts",
  },
  {
    kind: "merge",
    title: "Merge",
    description: "Mobius merges approved pull requests automatically.",
    line: "Autopilot paused: no automatic merge",
  },
  {
    kind: "repairs",
    title: "Repairs",
    description: "Mobius starts the tasks of CI breaks, check timeouts and CI failures.",
    line: "Repairs paused: no repair task starts",
  },
];

type Failure = { text: string; on: boolean };

export type ChangeSwitch = (kind: SwitchKind, on: boolean) => Promise<string>;

export function useSwitches(showLogin: () => void, source?: EventSource) {
  const [switches, setSwitches] = useState<Switches>();
  const [failures, setFailures] = useState<Partial<Record<SwitchKind, Failure>>>({});

  // An error belongs to the server value from the time of the failure. The first other value from the server clears it.
  const receive = useCallback((next: Switches) => {
    setSwitches(next);
    setFailures((current) => {
      const kept = Object.fromEntries(
        Object.entries(current).filter(
          ([kind, failure]) => next[kind as SwitchKind] === failure.on,
        ),
      );
      return Object.keys(kept).length === Object.keys(current).length ? current : kept;
    });
  }, []);

  const load = useCallback(
    () =>
      getSwitches()
        .then((res) => {
          if (res.status === 200) {
            receive(res.data.data);
            return res.data.data;
          }
        })
        .catch(() => undefined),
    [receive],
  );

  useEffect(() => {
    if (!source) {
      return;
    }
    // A switches event that comes while the connection is down is lost, so each connection reads the switches.
    void load();
    const reload = () => void load();
    source.addEventListener("open", reload);
    const remove = onEvent<LiveEvents, "switches">(source, "switches", receive);
    return () => {
      source.removeEventListener("open", reload);
      remove();
    };
  }, [source, load, receive]);

  // Gives the error text, or an empty text when the request has no error text to show, and the switches that the server gave after the failure.
  const send = (kind: SwitchKind, on: boolean) =>
    setSwitch(kind, { on })
      .then(async (res) => {
        if (res.status === 204) {
          return { text: "" };
        }
        if (res.status === 401) {
          showLogin();
          return { text: "", read: await load() };
        }
        return { text: res.data.error, read: await load() };
      })
      .catch(async (err: unknown) => ({ text: String(err), read: await load() }));

  // The amber line of a switch unmounts when the switch goes on, so the error stays here.
  const change: ChangeSwitch = (kind, on) => {
    setSwitches((current) => current && { ...current, [kind]: on });
    setFailures((current) =>
      Object.fromEntries(Object.entries(current).filter(([key]) => key !== kind)),
    );
    return send(kind, on).then(({ text, read }) => {
      const server = read?.[kind] ?? !on;
      if (text && server !== on) {
        setFailures((current) => ({ ...current, [kind]: { text, on: server } }));
      }
      return text;
    });
  };

  const errors: Partial<Record<SwitchKind, string>> = Object.fromEntries(
    Object.entries(failures).map(([kind, failure]) => [kind, failure.text]),
  );

  return { switches, errors, change };
}
