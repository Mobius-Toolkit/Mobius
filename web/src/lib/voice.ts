import { useEffect, useRef, useSyncExternalStore } from "react";
import {
  getCreateRecordingUrl,
  getFinishRecordingUrl,
  getSendChunkUrl,
  getTranscribeUrl,
  type createRecordingResponse,
  type finishRecordingResponse,
  type LiveText,
  type sendChunkResponse,
  type transcribeResponse,
} from "@/api/api.gen";
import { run as inDatabase } from "./database";
import workletUrl from "./recorder-worklet.ts?worker&url";

const sampleRate = 16000;
const maxRecording = 10 * 60 * 1000;
const chunkInterval = 2000;
const quickLimit = 30 * 1000;
// The server holds a conversion for up to 5 minutes.
const conversionLimit = 6 * 60 * 1000;
const takenMessage = "The recording stopped because another app took the microphone.";
const backgroundMessage = "The recording stopped because the app went to the background.";
const unreachableMessage =
  "The recording did not reach the server. Check the connection and try again.";
const emptyMessage = "The recording has no text.";
const lostMessage = "The last recording was not converted.";

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
// Each start and each cancel of a start changes run. A start of an older run does nothing.
let run = 0;
// The mounted inputs. The newest one gets the text. insertLive shows the live text in the input. It gives false when
// the input does not show it, because the user changed the field.
const inputs: {
  insert: (text: string, live: boolean) => void;
  insertLive: (text: string, first: boolean) => boolean;
}[] = [];
let screenLock: WakeLockSentinel | undefined;
// The WAV file of the last recording that the server did not convert to text, and whether its live text showed. The
// device also keeps the file in the store voice, so a reload does not lose it. A new recording, a discard and a
// conversion that gives text remove it.
let kept: { audio: Blob; shown: boolean } | undefined;
// The live text of the run that records or converts, and the call that waits for the text of that run. The text of a run
// with such a call goes to the call and not to the input.
let activeLive: Pick<Live, "shown" | "hidden"> | undefined;
let taker: ((text: string | undefined) => void) | undefined;

// The conversion works with no stored audio.
const storeAudio = (audio: Blob) =>
  inDatabase("voice", "readwrite", (store) => store.put({ key: "last", audio })).catch(() => {});

const removeAudio = () =>
  inDatabase("voice", "readwrite", (store) => store.delete("last")).catch(() => {});

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

// The request fails with a TimeoutError when it does not end within limit.
const post = async <T>(url: string, limit: number, init: RequestInit = {}) => {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(new DOMException("", "TimeoutError")), limit);
  try {
    const res = await fetch(url, { ...init, method: "POST", signal: controller.signal });
    return { status: res.status, data: await res.json() } as T;
  } finally {
    clearTimeout(timer);
  }
};

const convert = async (audio: Blob) => {
  try {
    const response = await post<transcribeResponse>(getTranscribeUrl(), conversionLimit, {
      headers: { "Content-Type": "audio/wav" },
      body: audio,
    });
    if (response.status === 200) {
      return { text: response.data.data.text.trim(), error: "" };
    }
    if (response.status === 502 || response.status === 503) {
      return { text: "", error: response.data.error };
    }
    return { text: "", error: "The server did not convert the recording." };
  } catch {
    return { text: "", error: unreachableMessage };
  }
};

const runLive = (live: Live, task: Promise<void>) => {
  live.busy = true;
  live.pending = task.finally(() => {
    live.busy = false;
  });
};

const openLive = async (live: Live) => {
  const response = await post<createRecordingResponse>(getCreateRecordingUrl(), quickLimit).catch(
    () => undefined,
  );
  if (response?.status === 200) {
    live.id = response.data.data.id;
  } else {
    live.failed = true;
  }
};

const showLive = (live: Live, data: LiveText) => {
  const text = [data.committed, data.draft].filter(Boolean).join(" ");
  const input = inputs.at(-1);
  if (!input || live.hidden || (!text && !live.shown)) {
    return;
  }
  if (input.insertLive(text, !live.shown)) {
    live.shown = true;
  } else {
    live.hidden = true;
  }
};

const sendNew = async (recording: Recording) => {
  const live = recording.live;
  const part = recording.chunks.slice(live.sent);
  if (part.length === 0) {
    return;
  }
  live.sent = recording.chunks.length;
  const response = await post<sendChunkResponse>(
    getSendChunkUrl(live.id, { sequence: live.sequence++ }),
    quickLimit,
    { headers: { "Content-Type": "application/octet-stream" }, body: new Blob(part) },
  ).catch(() => undefined);
  if (response?.status !== 200) {
    live.failed = true;
  } else {
    showLive(live, response.data.data);
  }
};

const tick = (recording: Recording) => {
  if (!recording.live.busy && !recording.live.failed) {
    runLive(recording.live, sendNew(recording));
  }
};

// convertLive sends the last samples and gets the text of the full recording. It gives nothing when the live text
// failed or the request failed, and then the caller sends the full recording as one file. A request that reaches its
// time limit gives the error, because the server can still hold the conversion.
const convertLive = async (recording: Recording) => {
  const live = recording.live;
  await live.pending;
  if (!live.failed) {
    await sendNew(recording);
  }
  if (live.failed) {
    return undefined;
  }
  try {
    const response = await post<finishRecordingResponse>(
      getFinishRecordingUrl(live.id),
      conversionLimit,
    );
    if (response.status === 200) {
      return { text: response.data.data.text.trim(), error: "" };
    }
    if (response.status === 502 || response.status === 503) {
      return { text: "", error: response.data.error };
    }
    return undefined;
  } catch (err) {
    return err instanceof DOMException && err.name === "TimeoutError"
      ? { text: "", error: unreachableMessage }
      : undefined;
  }
};

const settle = (
  result: { text: string; error: string },
  saved: NonNullable<typeof kept>,
  message: string,
) => {
  const error = result.error || (result.text ? "" : emptyMessage);
  kept = error ? saved : undefined;
  activeLive = undefined;
  const take = taker;
  taker = undefined;
  publish("idle", error || message);
  take?.(error ? undefined : result.text);
  if (!error) {
    void removeAudio();
    if (!take) {
      inputs.at(-1)?.insert(result.text, saved.shown);
    }
  }
};

const finish = async (message: string) => {
  const recording = current;
  if (!recording) {
    return;
  }
  current = undefined;
  publish("converting", message);
  clearTimeout(recording.timer);
  clearInterval(recording.live.timer);
  releaseScreen();
  await stopMedia(recording.stream, recording.context);
  const audio = wav(recording.chunks);
  await storeAudio(audio);
  const result = (await convertLive(recording)) ?? (await convert(audio));
  settle(result, { audio, shown: recording.live.shown }, message);
};

const resend = async () => {
  const saved = kept;
  if (!saved || snapshot.phase !== "idle") {
    return;
  }
  activeLive = { shown: saved.shown, hidden: false };
  publish("converting", "");
  settle(await convert(saved.audio), saved, "");
};

const discard = () => {
  if (!kept || snapshot.phase !== "idle") {
    return;
  }
  kept = undefined;
  void removeAudio();
  publish("idle", "");
};

inDatabase<{ audio: Blob } | undefined>("voice", "readonly", (store) => store.get("last")).then(
  (record) => {
    if (record && !kept && snapshot.phase === "idle" && run === 0) {
      kept = { audio: record.audio, shown: false };
      publish("idle", lostMessage);
    }
  },
  () => {},
);

const start = async () => {
  const token = ++run;
  kept = undefined;
  void removeAudio();
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
  activeLive = recording.live;
  runLive(recording.live, openLive(recording.live));
  recording.live.timer = setInterval(() => tick(recording), chunkInterval);
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

const cancelStart = () => {
  run++;
  releaseScreen();
  publish("idle", "");
};

// A tap on the converting button does nothing. A tap while the microphone starts cancels the start.
const toggleVoice = () => {
  if (snapshot.phase === "idle") {
    void start();
  } else if (snapshot.phase === "recording") {
    if (current) {
      void finish("");
    } else {
      cancelStart();
    }
  }
};

const stopVoice = () => {
  if (snapshot.phase === "recording") {
    toggleVoice();
  }
};

const showsLive = () =>
  snapshot.phase !== "idle" && activeLive !== undefined && activeLive.shown && !activeLive.hidden;

const finishForSend = () =>
  new Promise<string | undefined>((resolve) => {
    taker = resolve;
    stopVoice();
  });

export const voiceSupported =
  Boolean(navigator.mediaDevices?.getUserMedia) && typeof AudioWorkletNode !== "undefined";

// useVoice records the voice when toggle starts it. During the recording, the server gives the live text about each 2
// seconds, and it goes to onLive. onLive has first true for the first text of the run, and it gives false when it does
// not show the text. The second toggle stops the recording, and the server converts it to text. The text goes to onText
// of the input that is mounted when the answer comes. Its live is true when the run showed live text, and the text
// replaces it. The recording stays on until toggle or stop stops it, the recording reaches 10 minutes, or the
// microphone stops. A stop while the microphone starts cancels the start. Each request has a time limit, and a
// conversion with a failure or with an empty text gives an error. error is the failure of the last run, or '' when a
// run starts. The audio stays on the device from the stop until a conversion gives text, a run starts or discard
// removes it. After a failed conversion, or after a reload that finds the audio, canResend is true, and resend sends
// the audio again. When showsLive is true, the live text of the running run is in the input. Then finishForSend stops
// the recording or joins the conversion, and it gives the final text, or undefined when the conversion fails or the
// text is empty. The text of that run does not go to onText.
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
    const input = {
      insert: (text: string, live: boolean) => latestOnText.current(text, live),
      insertLive: (text: string, first: boolean) => latestOnLive.current(text, first),
    };
    inputs.push(input);
    return () => {
      inputs.splice(inputs.indexOf(input), 1);
    };
  }, []);

  return {
    supported: voiceSupported,
    recording: phase === "recording",
    converting: phase === "converting",
    error,
    canResend: snapshot.canResend,
    resend,
    discard,
    toggle: toggleVoice,
    stop: stopVoice,
    showsLive,
    finishForSend,
  };
}
