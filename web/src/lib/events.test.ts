import { Schema } from "effect";
import { describe, expect, it } from "vitest";
import { onEvent } from "./events";

const Count = Schema.Struct({ chat: Schema.String, count: Schema.Number });

describe("the live event listener", () => {
  it("gives the listener the decoded data of an event", () => {
    const source = new EventTarget();
    const heard: (typeof Count.Type)[] = [];
    onEvent(source, "unread", Count, (data) => heard.push(data));

    source.dispatchEvent(new MessageEvent("unread", { data: '{"chat":"a","count":2}' }));

    expect(heard).toEqual([{ chat: "a", count: 2 }]);
  });

  it("does not call the listener for data that does not decode", () => {
    const source = new EventTarget();
    const heard: (typeof Count.Type)[] = [];
    onEvent(source, "unread", Count, (data) => heard.push(data));

    source.dispatchEvent(new MessageEvent("unread", { data: '{"chat":"a","count":"two"}' }));
    source.dispatchEvent(new MessageEvent("unread", { data: "not json" }));

    expect(heard).toEqual([]);
  });

  it("ignores an event of another name and stops after the remove call", () => {
    const source = new EventTarget();
    const heard: (typeof Count.Type)[] = [];
    const remove = onEvent(source, "unread", Count, (data) => heard.push(data));

    source.dispatchEvent(new MessageEvent("inbox", { data: '{"chat":"a","count":2}' }));
    remove();
    source.dispatchEvent(new MessageEvent("unread", { data: '{"chat":"a","count":2}' }));

    expect(heard).toEqual([]);
  });
});
