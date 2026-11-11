import type { WorkstreamAutopilot } from "@/api/api.gen";

const names: Record<WorkstreamAutopilot, string> = {
  off: "Off",
  start: "Start tasks",
  merge: "Merge only",
  start_and_merge: "Start and merge",
};

export const levels: { value: WorkstreamAutopilot; name: string; text: string }[] = [
  { value: "off", name: names.off, text: "You start the tasks and merge them." },
  { value: "start", name: names.start, text: "Mobius starts the ready tasks. You merge." },
  {
    value: "merge",
    name: names.merge,
    text: "You start the tasks. Mobius merges after the Lead approves.",
  },
  {
    value: "start_and_merge",
    name: names.start_and_merge,
    text: "Mobius starts the ready tasks and merges after the Lead approves.",
  },
];

export function levelName(level: WorkstreamAutopilot) {
  return names[level];
}
