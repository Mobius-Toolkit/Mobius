import { useEffect, useRef, useSyncExternalStore } from "react";
import {
  createRecording,
  finishRecording,
  sendChunk,
  transcribe,
  type LiveText,
} from "@/api/api.gen";
import workletUrl from "./recorder-worklet.ts?worker&url";

const sampleRate = 16000;
const maxRecording = 10 * 60 * 1000;
const chunkInterval = 2000;
const takenMessage = "The recording stopped because another app took the microphone.";
const backgroundMessage = "The recording stopped because the app went to the background.";

type Phase = "idle" | "recording" | "converting";

// The live text of a recording. The server keeps the samples that the chunks carry. failed is true after a request
// fails. Then the run shows no more live text, and the stop sends the full recording as one file. hidden is true after
// the input refuses a live text.
type Live = {
  id: string;
  sequence: number;
  sent: number;
  pending: Promise<void>;
  busy: boolean;
  failed: boolean;
  shown: boolean;
  hidden: boolean;
  timer?: ReturnType<typeof setInterval>;
};

type Recording = {
  stream: MediaStream;
  context: AudioContext;
  chunks: Int16Array<ArrayBuffer>[];
  timer: ReturnType<typeof setTimeout>;
  live: Live;
};

// The run belongs to the page, not to a mount of useVoice. A switch of the Workstream remounts the input and does
// not stop the run.
let snapshot = { phase: "idle" as Phase, error: "", canResend: false };
const listeners = new Set<() => void>();
let current: Recording | undefined;
// Each start and each abort changes run. A step of an older run does nothing.
let run = 0;
// The input that gets the text of the answer.
let insert: ((text: string, live: boolean) => void) | undefined;
// Shows the live text in the input. It gives false when the input does not show it, because the user changed the field.
let insertLive: ((text: string, first: boolean) => boolean) | undefined;
let screenLock: WakeLockSentinel | undefined;
// The audio of the last recording that the server did not convert, and whether its live text showed. A new recording,
// a sent message and the end of a conversion remove it.
let kept: { chunks: Int16Array<ArrayBuffer>[]; shown: boolean } | undefined;

const publish = (phase: Phase, error: string) => {
  snapshot = { phase, error, canResend: kept !== undefined };
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

const runLive = (live: Live, task: Promise<void>) => {
  live.busy = true;
  live.pending = task.finally(() => {
    live.busy = false;
  });
};

const openLive = async (live: Live) => {
  const response = await createRecording().catch(() => undefined);
  if (response?.status === 200) {
    live.id = response.data.data.id;
  } else {
    live.failed = true;
  }
};

const showLive = (live: Live, data: LiveText) => {
  const text = [data.committed, data.draft].filter(Boolean).join(" ");
  if (!insertLive || live.hidden || (!text && !live.shown)) {
    return;
  }
  if (insertLive(text, !live.shown)) {
    live.shown = true;
  } else {
    live.hidden = true;
  }
};

const sendNew = async (recording: Recording, token: number) => {
  const live = recording.live;
  const part = recording.chunks.slice(live.sent);
  if (part.length === 0) {
    return;
  }
  live.sent = recording.chunks.length;
  const response = await sendChunk(live.id, new Blob(part), { sequence: live.sequence++ }).catch(
    () => undefined,
  );
  if (response?.status !== 200) {
    live.failed = true;
  } else if (token === run) {
    showLive(live, response.data.data);
  }
};

const tick = (recording: Recording, token: number) => {
  if (!recording.live.busy && !recording.live.failed) {
    runLive(recording.live, sendNew(recording, token));
  }
};

// convertLive sends the last samples and gets the text of the full recording. It gives nothing when the live text
// failed or the request failed, and then the caller sends the full recording as one file.
const convertLive = async (recording: Recording, token: number) => {
  const live = recording.live;
  await live.pending;
  if (!live.failed) {
    await sendNew(recording, token);
  }
  if (live.failed) {
    return undefined;
  }
  const response = await finishRecording(live.id).catch(() => undefined);
  if (response?.status === 200) {
    return { text: response.data.data.text.trim(), error: "" };
  }
  if (response?.status === 502 || response?.status === 503) {
    return { text: "", error: response.data.error };
  }
  return undefined;
};

const settle = (
  result: { text: string; error: string },
  saved: NonNullable<typeof kept>,
  message: string,
) => {
  kept = result.error ? saved : undefined;
  publish("idle", result.error || message);
  if (!result.error && (result.text || saved.shown)) {
    insert?.(result.text, saved.shown);
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
  clearInterval(recording.live.timer);
  releaseScreen();
  await stopMedia(recording.stream, recording.context);
  const result = (await convertLive(recording, token)) ?? (await convert(recording.chunks));
  if (token !== run) {
    return;
  }
  settle(result, { chunks: recording.chunks, shown: recording.live.shown }, message);
};

const resend = async () => {
  const saved = kept;
  if (!saved || snapshot.phase !== "idle") {
    return;
  }
  const token = run;
  publish("converting", "");
  const result = await convert(saved.chunks);
  if (token === run) {
    settle(result, saved, "");
  }
};

const start = async () => {
  const token = ++run;
  kept = undefined;
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
    live: {
      id: "",
      sequence: 0,
      sent: 0,
      pending: Promise.resolve(),
      busy: false,
      failed: false,
      shown: false,
      hidden: false,
    },
  };
  current = recording;
  runLive(recording.live, openLive(recording.live));
  recording.live.timer = setInterval(() => tick(recording, token), chunkInterval);
  const track = stream.getAudioTracks()[0];
  for (const type of ["mute", "ended"]) {
    track.addEventListener(type, () => {
      if (current === recording) {
        void finish(takenMessage);
      }
    });
  }
  if (document.visibilityState === "hidden") {
    void finish(backgroundMessage);
  } else if (track.muted || track.readyState === "ended") {
    void finish(takenMessage);
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
    clearInterval(current.live.timer);
    void stopMedia(current.stream, current.context);
    current = undefined;
  }
  releaseScreen();
  kept = undefined;
  publish("idle", "");
};

export const voiceSupported =
  Boolean(navigator.mediaDevices?.getUserMedia) && typeof AudioWorkletNode !== "undefined";

// useVoice records the voice when toggle starts it. During the recording, the server gives the live text about each 2
// seconds, and it goes to onLive. onLive has first true for the first text of the run, and it gives false when it does
// not show the text. The second toggle stops the recording, and the server converts it to text. The text goes to onText
// of the input that is mounted when the answer comes. Its live is true when the run showed live text, and the text
// replaces it. The recording stays on until toggle stops it, abort runs, the recording reaches 10 minutes, or the
// microphone stops. error is the failure of the last run, or '' when a run starts or abort runs. After a failed
// conversion, canResend is true, and resend sends the audio again until a conversion works, a run starts or abort runs.
export function useVoice(
  onText: (text: string, live: boolean) => void,
  onLive: (text: string, first: boolean) => boolean,
) {
  const { phase, error } = useSyncExternalStore(subscribe, () => snapshot);
  const latestOnText = useRef(onText);
  const latestOnLive = useRef(onLive);

  useEffect(() => {
    latestOnText.current = onText;
    latestOnLive.current = onLive;
  });

  useEffect(() => {
    const target = (text: string, live: boolean) => latestOnText.current(text, live);
    const targetLive = (text: string, first: boolean) => latestOnLive.current(text, first);
    insert = target;
    insertLive = targetLive;
    return () => {
      if (insert === target) {
        insert = undefined;
      }
      if (insertLive === targetLive) {
        insertLive = undefined;
      }
    };
  }, []);

  return {
    supported: voiceSupported,
    recording: phase === "recording",
    converting: phase === "converting",
    error,
    canResend: snapshot.canResend,
    resend,
    toggle: toggleVoice,
    abort: abortVoice,
  };
}
