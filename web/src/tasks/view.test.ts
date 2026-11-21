import { Option } from "effect";
import { inertHtml } from "foldkit/html";
import { describe, expect, it } from "vitest";
import { initialModel } from "./model";
import type { BoardTask } from "./schemas";
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

const render = (tasks: readonly BoardTask[]) =>
  JSON.stringify(
    view({ ...initialModel({ organization: "owner" }), tasks: Option.some(tasks) }, inertHtml),
  );

describe("the card of a task", () => {
  it("shows the total fix rounds and the total review rounds", () => {
    expect(render([task])).toContain("fix 5 · review 4");
  });

  it("shows no round text when the totals are 0", () => {
    const result = render([{ ...task, fixRounds: 0, totalFixRounds: 0, totalReviewRounds: 0 }]);
    expect(result).not.toContain("fix ");
    expect(result).not.toContain("review ");
  });
});
