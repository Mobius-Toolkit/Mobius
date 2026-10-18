import { useEffect, useRef, useSyncExternalStore } from "react";
import { transcribe } from "@/api/api.gen";
import workletUrl from "./recorder-worklet.ts?worker&url";

const sampleRate = 16000;
const maxRecording = 10 * 60 * 1000;
const takenMessage = "The recording stopped because another app took the microphone.";
const backgroundMessage = "The recording stopped because the app went to the background.";

type Phase = "idle" | "recording" | "converting";

type Recording = {
  stream: MediaStream;
  context: AudioContext;
  chunks: Int16Array<ArrayBuffer>[];
  timer: ReturnType<typeof setTimeout>;
};

// The run belongs to the page, not to a mount of useVoice. A switch of the Workstream remounts the input and does
// not stop the run.
let snapshot = { phase: "idle" as Phase, error: "" };
const listeners = new Set<() => void>();
let current: Recording | undefined;
// Each start and each abort changes run. A step of an older run does nothing.
let run = 0;
// The input that gets the text of the answer.
let insert: ((text: string) => void) | undefined;
let screenLock: WakeLockSentinel | undefined;

const publish = (phase: Phase, error: string) => {
  snapshot = { phase, error };
  listeners.forEach((listener) => listener());
};

const subscribe = (listener: () => void) => {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
};

// WebKit grants the lock only after a recent tap while the permission state is prompt.
// Thus the tap on the voice button requests the lock.
const holdScreen = () => {
  navigator.wakeLock?.request("screen").then(
    (lock) => {
      if (snapshot.phase !== "recording") {
        void lock.release();
        return;
      }
      const old = screenLock;
      screenLock = lock;
      void old?.release();
      lock.addEventListener("release", () => {
        if (screenLock === lock && document.visibilityState === "visible") {
          holdScreen();
        }
      });
    },
    () => {},
  );
};

const releaseScreen = () => {
  const lock = screenLock;
  screenLock = undefined;
  void lock?.release();
};

const stopMedia = (stream: MediaStream | undefined, context: AudioContext) => {
  stream?.getTracks().forEach((track) => track.stop());
  return context.close();
};

const startError = (err: unknown) => {
  const name = err instanceof DOMException ? err.name : "";
  if (name === "NotAllowedError") {
    return "The browser blocks the microphone. Allow the microphone in the browser settings.";
  }
  if (name === "NotFoundError") {
    return "The browser cannot find a microphone. Connect a microphone and try again.";
  }
  return "The voice input did not start.";
};

const wav = (chunks: Int16Array<ArrayBuffer>[]) => {
  const size = chunks.reduce((sum, chunk) => sum + chunk.byteLength, 0);
  const header = new DataView(new ArrayBuffer(44));
  const text = (offset: number, value: string) =>
    [...value].forEach((letter, i) => header.setUint8(offset + i, letter.charCodeAt(0)));
  text(0, "RIFF");
  header.setUint32(4, 36 + size, true);
  text(8, "WAVEfmt ");
  header.setUint32(16, 16, true);
  header.setUint16(20, 1, true);
  header.setUint16(22, 1, true);
  header.setUint32(24, sampleRate, true);
  header.setUint32(28, sampleRate * 2, true);
  header.setUint16(32, 2, true);
  header.setUint16(34, 16, true);
  text(36, "data");
  header.setUint32(40, size, true);
  return new Blob([header, ...chunks], { type: "audio/wav" });
};

const convert = async (chunks: Int16Array<ArrayBuffer>[]) => {
  try {
    const response = await transcribe(wav(chunks));
    if (response.status === 200) {
      return { text: response.data.data.text.trim(), error: "" };
    }
    if (response.status === 502 || response.status === 503) {
      return { text: "", error: response.data.error };
    }
    return { text: "", error: "The server did not convert the recording." };
  } catch {
    return {
      text: "",
      error: "The recording did not reach the server. Check the connection and try again.",
    };
  }
};

const finish = async (message: string) => {
  const recording = current;
  if (!recording) {
    return;
  }
  current = undefined;
  const token = run;
  publish("converting", message);
  clearTimeout(recording.timer);
  releaseScreen();
  await stopMedia(recording.stream, recording.context);
  const result = await convert(recording.chunks);
  if (token !== run) {
    return;
  }
  publish("idle", result.error || message);
  if (result.text) {
    insert?.(result.text);
  }
};

const start = async () => {
  const token = ++run;
  const context = new AudioContext();
  void context.resume();
  publish("recording", "");
  holdScreen();
  let stream: MediaStream | undefined;
  const chunks: Int16Array<ArrayBuffer>[] = [];
  try {
    stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    await context.audioWorklet.addModule(workletUrl);
    const node = new AudioWorkletNode(context, "recorder");
    node.port.addEventListener("message", (event: MessageEvent<Int16Array<ArrayBuffer>>) =>
      chunks.push(event.data),
    );
    node.port.start();
    context.createMediaStreamSource(stream).connect(node);
  } catch (err) {
    void stopMedia(stream, context);
    if (token === run) {
      releaseScreen();
      publish("idle", startError(err));
    }
    return;
  }
  if (token !== run) {
    void stopMedia(stream, context);
    return;
  }
  const recording: Recording = {
    stream,
    context,
    chunks,
    timer: setTimeout(() => void finish(""), maxRecording),
  };
  current = recording;
  for (const type of ["mute", "ended"]) {
    stream.getAudioTracks()[0].addEventListener(type, () => {
      if (current === recording) {
        void finish(takenMessage);
      }
    });
  }
};

document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "hidden") {
    void finish(backgroundMessage);
  }
});

// A tap on the converting button does nothing. A tap while the microphone starts cancels the start.
const toggleVoice = () => {
  if (snapshot.phase === "idle") {
    void start();
  } else if (snapshot.phase === "recording") {
    if (current) {
      void finish("");
    } else {
      abortVoice();
    }
  }
};

// abortVoice drops the recording or the answer that the run still holds.
const abortVoice = () => {
  run++;
  if (current) {
    clearTimeout(current.timer);
    void stopMedia(current.stream, current.context);
    current = undefined;
  }
  releaseScreen();
  publish("idle", "");
};

export const voiceSupported =
  Boolean(navigator.mediaDevices?.getUserMedia) && typeof AudioWorkletNode !== "undefined";

// useVoice records the voice when toggle starts it. The second toggle stops the recording, and the server converts it
// to text. The text goes to onText of the input that is mounted when the answer comes. The recording stays on until
// toggle stops it, abort runs, the recording reaches 10 minutes, or the microphone stops. error is the failure of the
// last run, or '' when a run starts or abort runs.
export function useVoice(onText: (text: string) => void) {
  const { phase, error } = useSyncExternalStore(subscribe, () => snapshot);
  const latestOnText = useRef(onText);

  useEffect(() => {
    latestOnText.current = onText;
  });

  useEffect(() => {
    const target = (text: string) => latestOnText.current(text);
    insert = target;
    return () => {
      if (insert === target) {
        insert = undefined;
      }
    };
  }, []);

  return {
    supported: voiceSupported,
    recording: phase === "recording",
    converting: phase === "converting",
    error,
    toggle: toggleVoice,
    abort: abortVoice,
  };
}
