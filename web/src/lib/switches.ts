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

export type ChangeSwitch = (kind: SwitchKind, on: boolean) => Promise<string>;

export function useSwitches(showLogin: () => void, source?: EventSource) {
  const [switches, setSwitches] = useState<Switches>();
  const [errors, setErrors] = useState<Partial<Record<SwitchKind, string>>>({});

  const load = useCallback(
    () =>
      getSwitches()
        .then((res) => {
          if (res.status === 200) {
            setSwitches(res.data.data);
          }
        })
        .catch(() => {}),
    [],
  );

  useEffect(() => {
    if (!source) {
      return;
    }
    // A switches event that comes while the connection is down is lost, so each connection reads the switches.
    void load();
    const reload = () => void load();
    source.addEventListener("open", reload);
    const remove = onEvent<LiveEvents, "switches">(source, "switches", setSwitches);
    return () => {
      source.removeEventListener("open", reload);
      remove();
    };
  }, [source, load]);

  // Gives the error text, or an empty text when the server turned the switch on or off.
  const send = (kind: SwitchKind, on: boolean) =>
    setSwitch(kind, { on })
      .then(async (res) => {
        if (res.status === 204) {
          return "";
        }
        if (res.status === 401) {
          showLogin();
          await load();
          return "";
        }
        await load();
        return res.data.error;
      })
      .catch(async (err: unknown) => {
        await load();
        return String(err);
      });

  // The amber line of a switch unmounts when the switch goes on, so the error stays here.
  const change: ChangeSwitch = (kind, on) => {
    setSwitches((current) => current && { ...current, [kind]: on });
    setErrors((current) => ({ ...current, [kind]: "" }));
    return send(kind, on).then((text) => {
      setErrors((current) => ({ ...current, [kind]: text }));
      return text;
    });
  };

  return { switches, errors, change };
}
