import { Option } from "effect";
import { describe, expect, it } from "vitest";
import { Command as StoryCommand, given, message, model, story } from "foldkit/story";
import { lowLoadReason } from "@/lib/agents";
import * as TranscriptFixture from "@/transcript/fixtures";
import type { Agent } from "@/transcript/schemas";
import { FetchAgents, ReportTitle, RequestLogin } from "./commands";
import { Message, toGotTranscriptMessage } from "./message";
import { initialModel, type Model } from "./model";
import type { ActiveAgents } from "./schemas";
import { update } from "./update";

const agent = (overrides: Partial<Agent> = {}): Agent => ({
  effort: "high",
  endReason: "",
  endedAt: null,
  harness: "devin",
  id: 7,
  issue: 39,
  model: "swe-1.5",
  name: "Implementer",
  organization: "owner",
  parent: null,
  pausedUntil: null,
  queueReason: "",
  repository: "owner/shop",
  role: "implementer",
  startedAt: "2026-05-01T10:00:00Z",
  title: "Task 39",
  working: true,
  workstream: 12,
  workstreamRepository: "owner/shop",
  ...overrides,
});

const listOf = (...agents: Agent[]): ActiveAgents => ({
  groups: [
    {
      name: "Implementer",
      count: agents.length,
      max: 2,
      agents: agents.map((row) => ({
        agent: row,
        issueTitle: null,
        pullRequest: null,
        workstreamTitle: null,
      })),
    },
  ],
});

const createModel = (overrides: Partial<Model> = {}): Model => ({
  ...initialModel,
  ...overrides,
});

describe("the list of the agents", () => {
  it("reads the list when the connection opens, and shows the answer", () => {
    story(
      update,
      given(createModel()),
      message(Message.ConnectionOpened()),
      model((m) => {
        expect(m.request).toBe(2);
      }),
      StoryCommand.expectExact(FetchAgents({ request: 2 })),
      StoryCommand.resolve(
        FetchAgents,
        Message.AgentsLoaded({ request: 2, agents: listOf(agent()) }),
      ),
      model((m) => {
        expect(Option.isSome(m.agents)).toBe(true);
      }),
    );
  });

  it("ignores an answer to an older request", () => {
    story(
      update,
      given(createModel({ request: 3 })),
      message(Message.AgentsLoaded({ request: 2, agents: listOf(agent()) })),
      model((m) => {
        expect(Option.isNone(m.agents)).toBe(true);
      }),
      message(Message.LoadFailed({ request: 2, error: "old" })),
      model((m) => {
        expect(m.error).toBe("");
      }),
      message(Message.LoadFailed({ request: 3, error: "new" })),
      model((m) => {
        expect(m.error).toBe("new");
      }),
    );
  });

  it("reads the list again after a change of an agent", () => {
    story(
      update,
      given(createModel()),
      message(Message.AgentChanged()),
      StoryCommand.expectExact(FetchAgents({ request: 2 })),
      StoryCommand.resolve(FetchAgents, Message.LoadFailed({ request: 2, error: "down" })),
    );
  });

  it("asks the host for the login on a 401", () => {
    story(
      update,
      given(createModel()),
      message(Message.LoginRequired()),
      StoryCommand.expectExact(RequestLogin()),
      StoryCommand.resolve(RequestLogin, Message.CompletedRequestLogin()),
    );
  });
});

describe("the Transcript of an agent", () => {
  it("opens the Transcript, and tells the host the title", () => {
    story(
      update,
      given(createModel()),
      message(Message.OpenedAgent({ agent: agent() })),
      model((m) => {
        expect(Option.getOrThrow(m.transcript).agent.id).toBe(7);
      }),
      StoryCommand.expectExact(ReportTitle({ title: "Implementer Task 39" })),
      StoryCommand.resolve(ReportTitle, Message.CompletedReportTitle()),
    );
  });

  it("closes the Transcript with Back and with the Back of the browser, and clears the title", () => {
    story(
      update,
      given(createModel()),
      message(Message.OpenedAgent({ agent: agent() })),
      StoryCommand.resolve(ReportTitle, Message.CompletedReportTitle()),
      message(Message.ClickedBack()),
      model((m) => {
        expect(Option.isNone(m.transcript)).toBe(true);
      }),
      StoryCommand.expectExact(ReportTitle({ title: "" })),
      StoryCommand.resolve(ReportTitle, Message.CompletedReportTitle()),
      message(Message.OpenedAgent({ agent: agent() })),
      StoryCommand.resolve(ReportTitle, Message.CompletedReportTitle()),
      message(Message.BackRequested()),
      model((m) => {
        expect(Option.isNone(m.transcript)).toBe(true);
      }),
      StoryCommand.expectExact(ReportTitle({ title: "" })),
      StoryCommand.resolve(ReportTitle, Message.CompletedReportTitle()),
    );
  });

  it("closes the Transcript when the Transcript asks for it", () => {
    story(
      update,
      given(createModel()),
      message(Message.OpenedAgent({ agent: agent() })),
      StoryCommand.resolve(ReportTitle, Message.CompletedReportTitle()),
      message(toGotTranscriptMessage(TranscriptFixture.closed)),
      model((m) => {
        expect(Option.isNone(m.transcript)).toBe(true);
      }),
      StoryCommand.expectExact(ReportTitle({ title: "" })),
      StoryCommand.resolve(ReportTitle, Message.CompletedReportTitle()),
    );
  });

  it("asks the host for the login when the Transcript gets a 401", () => {
    story(
      update,
      given(createModel()),
      message(Message.OpenedAgent({ agent: agent() })),
      StoryCommand.resolve(ReportTitle, Message.CompletedReportTitle()),
      message(toGotTranscriptMessage(TranscriptFixture.loginRequired)),
      StoryCommand.expectExact(RequestLogin()),
      StoryCommand.resolve(RequestLogin, Message.CompletedRequestLogin()),
    );
  });

  it("shows the start control while the open agent waits for a low load, and hides it after", () => {
    const waiting = agent({ queueReason: lowLoadReason });

    story(
      update,
      given(createModel({ request: 1 })),
      message(Message.OpenedAgent({ agent: agent() })),
      StoryCommand.resolve(ReportTitle, Message.CompletedReportTitle()),
      message(Message.AgentsLoaded({ request: 1, agents: listOf(waiting) })),
      model((m) => {
        expect(Option.getOrThrow(m.transcript).startable).toBe(true);
      }),
      message(Message.AgentsLoaded({ request: 1, agents: listOf(agent()) })),
      model((m) => {
        expect(Option.getOrThrow(m.transcript).startable).toBe(false);
      }),
      message(Message.AgentsLoaded({ request: 1, agents: listOf(waiting) })),
      message(Message.AgentsLoaded({ request: 1, agents: { groups: [] } })),
      model((m) => {
        expect(Option.getOrThrow(m.transcript).startable).toBe(false);
      }),
    );
  });
});
