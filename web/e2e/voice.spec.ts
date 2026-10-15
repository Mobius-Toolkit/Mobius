import { expect, test } from "@playwright/test";

// The voice input has one run for the whole page. These tests use the run with two fake inputs, as two
// MessageInput components on one page use it.
const calls: string[] = [];
const recognitions: EventTarget[] = [];

class FakeRecognition extends EventTarget {
  constructor() {
    super();
    recognitions.push(this);
  }
  start() {
    calls.push("start");
  }
  stop() {
    calls.push("stop");
  }
  abort() {
    calls.push("abort");
  }
}

Object.assign(globalThis, {
  window: globalThis,
  document: { addEventListener: () => {}, visibilityState: "visible" },
  SpeechRecognition: FakeRecognition,
});

const { abortVoice, toggleVoice } = await import("../src/lib/voice.js");

function fakeInput() {
  const input = { listening: false, texts: [] as string[] };
  const target = {
    onText: (text: string) => input.texts.push(text),
    touched: () => false,
    setListening: (listening: boolean) => {
      input.listening = listening;
    },
    setError: () => {},
  };
  return { input, target };
}

test("a start of a second input stops the voice input of the first input", () => {
  const first = fakeInput();
  const second = fakeInput();
  toggleVoice(first.target);
  expect(first.input.listening).toBe(true);
  recognitions[0].dispatchEvent(new Event("audiostart"));

  toggleVoice(second.target);
  expect(first.input.listening).toBe(false);
  expect(second.input.listening).toBe(true);
  expect(calls).toEqual(["start", "stop"]);

  recognitions[0].dispatchEvent(new Event("end"));
  expect(calls).toEqual(["start", "stop", "start"]);
  recognitions[0].dispatchEvent(
    Object.assign(new Event("result"), {
      results: [Object.assign([{ transcript: "red roses" }], { isFinal: true })],
    }),
  );
  expect(second.input.texts).toEqual(["red roses"]);
  expect(first.input.texts).toEqual([]);
});

test("an abort by an input that does not own the run keeps the run", () => {
  const first = fakeInput();
  const second = fakeInput();
  toggleVoice(second.target);
  const before = calls.length;
  abortVoice(first.target);
  expect(calls.length).toBe(before);
  expect(second.input.listening).toBe(true);
  abortVoice(second.target);
  expect(calls.at(-1)).toBe("abort");
  expect(second.input.listening).toBe(false);
});
