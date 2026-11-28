import type { AgentWait } from "@/api/api.gen";
import { dayClock } from "./time";

export type StateInput = {
  state: string;
  merging: boolean;
  wait: AgentWait;
  pausedUntil: string | null;
};

const waitTexts: Record<Exclude<AgentWait, "" | "paused">, string> = {
  start_off: "start paused",
  drain: "waits for upgrade",
  no_access: "no access",
  slot: "queued",
  check_slot: "waits for check",
  low_load: "waits for load",
  local_check: "checks",
  init: "setup",
  harness_start: "starting",
  ci: "waits for CI",
  lead: "waits for Lead",
  owner: "waits for Owner",
  start_implementer: "waits for start_implementer",
  owner_action: "waits for Owner action",
};

export const pausedText = (pausedUntil: string) => `paused until ${dayClock(pausedUntil)}`;

export const networkText = (lostAt: string) =>
  `No network since ${dayClock(lostAt)}. Mobius waits for the network.`;

export function displayState({ state, merging, wait, pausedUntil }: StateInput) {
  if (wait === "") {
    return { text: merging ? "approved" : state, variant: "secondary" as const };
  }
  if (wait === "paused") {
    return {
      text: pausedUntil ? pausedText(pausedUntil) : "paused",
      variant: "outline" as const,
    };
  }
  return { text: waitTexts[wait], variant: "outline" as const };
}

export const waitDot = (wait: AgentWait) =>
  wait === "" || wait === "local_check" || wait === "init" ? "bg-green-600" : "bg-amber-500";
