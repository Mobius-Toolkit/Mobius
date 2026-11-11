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
