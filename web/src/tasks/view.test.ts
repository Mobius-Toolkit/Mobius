import { Option } from "effect";
import * as Scene from "foldkit/scene";
import { describe, it } from "vitest";
import { Navigate } from "./commands";
import { Message } from "./message";
import { initialModel } from "./model";
import type { BoardTask } from "./schemas";
import { defineRouterLink } from "@/ui/link";
import { update } from "./update";
import { view } from "./view";

const task: BoardTask = {
  repository: "owner/shop",
  number: 41,
  title: "Add plan model",
  url: "https://github.com/owner/shop/issues/41",
  workstreamRepository: "owner/shop",
  workstream: 12,
  workstreamTitle: "Integrate loyalty plans",
  state: "working",
  stateAt: "2026-10-15T11:00:00Z",
  pullRequest: null,
  pullRequestUrl: null,
  fixRounds: 1,
  reviewRounds: 0,
  totalFixRounds: 5,
  totalReviewRounds: 4,
  merging: false,
  mergeRefusal: "",
  mergeQueuePlace: null,
  mergeQueueSize: 0,
};

const RouterLink = defineRouterLink(Message.NavigateTo);

const board = (tasks: readonly BoardTask[]) => ({
  ...initialModel({ organization: "owner" }),
  tasks: Option.some(tasks),
});

describe("the card of a task", () => {
  it("shows the total fix rounds and the total review rounds", () => {
    Scene.scene(
      { update, view },
      Scene.given(board([task])),
      Scene.Mount.resolve(RouterLink, Message.NavigateTo({ path: "/workstreams/owner/shop/12" })),
      Scene.Command.resolve(Navigate, Message.CompletedNavigate()),
      Scene.expect(Scene.text("fix 5 · review 4")).toExist(),
    );
  });
});
