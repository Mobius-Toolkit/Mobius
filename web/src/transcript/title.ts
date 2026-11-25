import { bindingText } from "@/lib/harness";
import { issueRef } from "@/lib/notice";
import type { Agent } from "./schemas";

const roleText = (agent: Agent) =>
  [`${agent.name.charAt(0).toUpperCase()}${agent.name.slice(1)}`, agent.title]
    .filter(Boolean)
    .join(" ");

export function transcriptTitle(agent: Agent): string {
  if (agent.issue === null) {
    return roleText(agent);
  }
  const reference = issueRef(agent.repository, agent.issue, agent.workstreamRepository);
  return agent.issueTitle === null ? reference : `${reference} ${agent.issueTitle}`;
}

export function transcriptSubtitle(agent: Agent): string {
  const binding = bindingText(agent);
  return agent.issue === null ? binding : `${roleText(agent)} · ${binding}`;
}
