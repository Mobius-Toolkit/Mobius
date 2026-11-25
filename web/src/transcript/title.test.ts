import { describe, expect, it } from "vitest";
import type { Agent } from "./schemas";
import { transcriptSubtitle, transcriptTitle } from "./title";

const agent = (overrides: Partial<Agent> = {}): Agent => ({
  effort: "medium",
  endReason: "",
  endedAt: null,
  harness: "claude-code",
  id: 7,
  issue: 41,
  issueTitle: "Add plan model",
  keptUntil: null,
  model: "opus",
  name: "implementer",
  organization: "owner",
  parent: null,
  pausedUntil: null,
  queueReason: "",
  repository: "owner/shop",
  role: "implementer",
  startedAt: "2026-05-01T10:00:00Z",
  title: "",
  wait: "",
  working: true,
  workstream: 12,
  workstreamRepository: "owner/shop",
  ...overrides,
});

describe("the title of the Transcript of an agent", () => {
  it("is the reference and the issue title for an issue in the repository of its Workstream", () => {
    expect(transcriptTitle(agent())).toBe("#41 Add plan model");
  });

  it("has the repository for an issue in another repository than its Workstream", () => {
    expect(transcriptTitle(agent({ repository: "owner/site" }))).toBe(
      "owner/site#41 Add plan model",
    );
  });

  it("is only the reference when the issue title is null", () => {
    expect(transcriptTitle(agent({ issueTitle: null }))).toBe("#41");
  });

  it("is the role and the title of an agent with no issue", () => {
    const lead = agent({ issue: null, issueTitle: null, name: "Lead", title: "chat session" });
    expect(transcriptTitle(lead)).toBe("Lead chat session");
    expect(transcriptTitle(agent({ issue: null, issueTitle: null, name: "researcher" }))).toBe(
      "Researcher",
    );
  });
});

describe("the subtitle of the Transcript of an agent", () => {
  it("is the role and the binding for an agent with an issue", () => {
    expect(transcriptSubtitle(agent())).toBe("Implementer · claude-code · opus · medium");
  });

  it("has the title of the agent after the role", () => {
    expect(transcriptSubtitle(agent({ title: "owner/shop" }))).toBe(
      "Implementer owner/shop · claude-code · opus · medium",
    );
  });

  it("is only the binding for an agent with no issue", () => {
    const lead = agent({ issue: null, issueTitle: null, name: "Lead", title: "chat session" });
    expect(transcriptSubtitle(lead)).toBe("claude-code · opus · medium");
  });
});
