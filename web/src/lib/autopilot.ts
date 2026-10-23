import type { WorkstreamAutopilot } from "@/api/api.gen";

export const levels: { value: WorkstreamAutopilot; name: string; text: string }[] = [
  { value: "off", name: "Off", text: "You start the tasks and merge them." },
  { value: "start", name: "Start tasks", text: "Mobius starts the ready tasks. You merge." },
  {
    value: "merge",
    name: "Merge only",
    text: "You start the tasks. Mobius merges after the Lead approves.",
  },
  {
    value: "start_and_merge",
    name: "Start and merge",
    text: "Mobius starts the ready tasks and merges after the Lead approves.",
  },
];

export function levelName(level: WorkstreamAutopilot) {
  return levels.find((other) => other.value === level)!.name;
}
