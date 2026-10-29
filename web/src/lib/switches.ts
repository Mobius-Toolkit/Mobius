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

  // Gives the error text of a failed request, or undefined when the request succeeded. A 401 gives an empty text.
  const send = (kind: SwitchKind, on: boolean) =>
    setSwitch(kind, { on })
      .then((res) => {
        if (res.status === 204) {
          return undefined;
        }
        if (res.status === 401) {
          showLogin();
          return "";
        }
        return res.data.error;
      })
      .catch((err: unknown) => String(err));

  // The amber line of a switch unmounts when the switch goes on, so the error stays here.
  const change: ChangeSwitch = (kind, on) => {
    setSwitches((current) => current && { ...current, [kind]: on });
    setFailures((current) =>
      Object.fromEntries(Object.entries(current).filter(([key]) => key !== kind)),
    );
    return send(kind, on).then(async (text) => {
      if (text === undefined) {
        return "";
      }
      // The read can be late or fail, so the failure shows at once with the value from before the request. The read then replaces that value.
      setSwitches((current) => current && { ...current, [kind]: !on });
      if (text) {
        setFailures((current) => ({ ...current, [kind]: { text, on: !on } }));
      }
      await load();
      return text;
    });
  };

  const errors: Partial<Record<SwitchKind, string>> = Object.fromEntries(
    Object.entries(failures).map(([kind, failure]) => [kind, failure.text]),
  );

  return { switches, errors, change };
}
