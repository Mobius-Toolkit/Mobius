import { Schema } from "effect";
import { Message } from "./selection";

const decode = Schema.decodeUnknownSync(Message);

export const rect = { left: 10, right: 60, top: 20, bottom: 36 };

export const selected = decode({
  _tag: "Selected",
  owner: "brief",
  text: "free plan",
  context: "One free plan for staff",
  part: "Scope",
  rect,
});

export const addClicked = decode({ _tag: "AddClicked" });

export const clearedSelection = decode({ _tag: "CompletedClearSelection" });

export const selectedInTask = decode({
  _tag: "Selected",
  owner: "task",
  text: "free plan",
  context: "One free plan for staff",
  part: "Scope",
  rect,
});
