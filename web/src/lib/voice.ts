import { useEffect, useRef, useSyncExternalStore } from "react";
import {
  getCreateRecordingUrl,
  getFinishRecordingUrl,
  getGetConversionUrl,
  getSendChunkUrl,
  getTranscribeUrl,
  type createRecordingResponse200,
  type finishRecordingResponse200,
  type getConversionResponse200,
  type LiveText,
  type sendChunkResponse200,
  type transcribeResponse200,
} from "@/api/api.gen";
import {
  convertingDraftKeys,
  currentDraft,
  loadDraft,
  updateDraft,
  type DraftVoice,
} from "./draft";
import workletUrl from "./recorder-worklet.ts?worker&url";

const sampleRate = 16000;
const maxRecording = 10 * 60 * 1000;
const chunkInterval = 2000;
const quickLimit = 30 * 1000;
const uploadLimit = 6 * 60 * 1000;
const conversionInterval = 2000;
const waitLimit = 16 * 60 * 1000;
const takenMessage = "The recording stopped because another app took the microphone.";
const backgroundMessage = "The recording stopped because the app went to the background.";
const unreachableMessage =
  "The recording did not reach the server. Check the connection and try again.";
const unconvertedMessage = "The server did not convert the recording.";
const emptyMessage = "The recording has no text.";

type Result = { text: string; error: string };

// The live text of a recording. The server keeps the samples that the chunks carry. failed is true after a request
// fails. Then the run shows no more live text, and the stop sends the full recording as one file.
type Live = {
  id: string;
  sequence: number;
  sent: number;
  pending: Promise<void>;
  busy: boolean;
  failed: boolean;
  timer?: ReturnType<typeof setInterval>;
};

type Recording = {
  key: string;
  stream: MediaStream;
  context: AudioContext;
  chunks: Int16Array<ArrayBuffer>[];
  timer: ReturnType<typeof setTimeout>;
  live: Live;
};

// The run belongs to the page, not to a mount of useVoice. key is the draft key of the input where the run started, and
// live is its live text. notice is the message of a start that failed or of a stop that the user did not ask for. It
// belongs to the draft key of its run.
let snapshot = {
  recording: false,
  key: "",
  startedAt: 0,
  live: "",
  notice: { key: "", text: "" },
};
const listeners = new Set<() => void>();
let current: Recording | undefined;
// True from the stop of a recording until its card is in the draft.
let stopping = false;
// Each start and each cancel of a start changes run. A start of an older run does nothing.
let run = 0;
// The mounted inputs. insert puts the text of a conversion at the cursor of the field.
const inputs: { key: string; insert: (text: string) => void }[] = [];
let screenLock: WakeLockSentinel | undefined;
// The draft keys that have a call that converts their card.
const converting = new Set<string>();

const publish = (patch: Partial<typeof snapshot>) => {
  snapshot = { ...snapshot, ...patch };
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
      if (!snapshot.recording) {
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

type Answer<T> = { status: number; data?: T & { error?: string } };

// The request fails with a TimeoutError when it does not end within limit. data is undefined when the answer is not
// JSON.
const request = async <T>(
  url: string,
  limit: number,
  init: RequestInit = {},
): Promise<Answer<T>> => {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(new DOMException("", "TimeoutError")), limit);
  try {
    const res = await fetch(url, { ...init, signal: controller.signal });
    return { status: res.status, data: await res.json().catch(() => undefined) };
  } finally {
    clearTimeout(timer);
  }
};

const post = <T>(url: string, limit: number, init: RequestInit = {}) =>
  request<T>(url, limit, { ...init, method: "POST" });

const failure = (answer: Answer<unknown>) =>
  answer.data?.error ?? `The server answered ${answer.status}.`;

const pause = (time: number) => new Promise((resolve) => setTimeout(resolve, time));

const setVoice = (key: string, change: (voice: DraftVoice) => DraftVoice) =>
  updateDraft(key, (draft) => (draft.voice ? { ...draft, voice: change(draft.voice) } : draft));

// awaitConversion asks for the conversion each conversionInterval until it ends. A request that fails does not end
// the conversion, but a 4xx answer other than 404 does. A conversion that the server lost starts again with the audio.
// The wait ends with the last failure after waitLimit.
const awaitConversion = async (key: string, id: string, audio: Blob): Promise<Result> => {
  const end = Date.now() + waitLimit;
  let last = unreachableMessage;
  while (Date.now() < end) {
    await pause(conversionInterval);
    const answer = await request<getConversionResponse200["data"]>(
      getGetConversionUrl(id),
      quickLimit,
    ).catch(() => undefined);
    if (answer?.status === 404) {
      return convert(key, audio);
    }
    if (answer && answer.status >= 400 && answer.status < 500) {
      return { text: "", error: failure(answer) };
    }
    last = !answer
      ? unreachableMessage
      : answer.status === 200
        ? unconvertedMessage
        : failure(answer);
    const conversion = answer?.status === 200 ? answer.data?.data : undefined;
    if (conversion?.state === "done") {
      return { text: conversion.text.trim(), error: "" };
    }
    if (conversion?.state === "failed") {
      return { text: "", error: conversion.error };
    }
  }
  return { text: "", error: last };
};

const convert = async (key: string, audio: Blob): Promise<Result> => {
  try {
    const answer = await post<transcribeResponse200["data"]>(getTranscribeUrl(), uploadLimit, {
      headers: { "Content-Type": "audio/wav" },
      body: audio,
    });
    if (answer.status === 200 && answer.data) {
      const { id } = answer.data.data;
      await setVoice(key, (voice) => ({ ...voice, conversion: id }));
      return await awaitConversion(key, id, audio);
    }
    return { text: "", error: failure(answer) };
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
  const response = await post<createRecordingResponse200["data"]>(
    getCreateRecordingUrl(),
    quickLimit,
  ).catch(() => undefined);
  if (response?.status === 200 && response.data) {
    live.id = response.data.data.id;
  } else {
    live.failed = true;
  }
};

const showLive = (recording: Recording, data: LiveText) => {
  if (current === recording) {
    publish({ live: [data.committed, data.draft].filter(Boolean).join(" ") });
  }
};

const sendNew = async (recording: Recording) => {
  const live = recording.live;
  const part = recording.chunks.slice(live.sent);
  if (part.length === 0) {
    return;
  }
  live.sent = recording.chunks.length;
  const response = await post<sendChunkResponse200["data"]>(
    getSendChunkUrl(live.id, { sequence: live.sequence++ }),
    quickLimit,
    { headers: { "Content-Type": "application/octet-stream" }, body: new Blob(part) },
  ).catch(() => undefined);
  if (response?.status !== 200 || !response.data) {
    live.failed = true;
  } else {
    showLive(recording, response.data.data);
  }
};

const tick = (recording: Recording) => {
  if (!recording.live.busy && !recording.live.failed) {
    runLive(recording.live, sendNew(recording));
  }
};

// finishLive sends the last samples and gives the id of the conversion of the full recording. It gives nothing when
// the live text failed or the request failed, and then the conversion of the card sends the full recording as one file.
const finishLive = async (recording: Recording) => {
  const live = recording.live;
  await live.pending;
  if (!live.failed) {
    await sendNew(recording);
  }
  if (live.failed) {
    return undefined;
  }
  const answer = await post<finishRecordingResponse200["data"]>(
    getFinishRecordingUrl(live.id),
    quickLimit,
  ).catch(() => undefined);
  if (answer?.status !== 200 || !answer.data) {
    return undefined;
  }
  return answer.data.data.id;
};

// Gives the text with voiceText at the selection from start to end, and the cursor after voiceText. A space separates
// voiceText from a word next to it.
export const insertVoiceText = (
  text: string,
  voiceText: string,
  start = text.length,
  end = start,
) => {
  const before = text.slice(0, start);
  const after = text.slice(end);
  const lead = before === "" || /\s$/.test(before) ? before : `${before} `;
  const trail = after === "" || /^\s/.test(after) ? after : ` ${after}`;
  return { text: lead + voiceText + trail, cursor: lead.length + voiceText.length };
};

const settle = async (key: string, result: Result) => {
  if (currentDraft(key)?.voice?.state !== "converting") {
    return;
  }
  const error = result.error || (result.text ? "" : emptyMessage);
  if (error) {
    await setVoice(key, (voice) => ({ ...voice, state: "failed", conversion: "", error }));
    return;
  }
  const ready: DraftVoice = { state: "ready", audio: null, conversion: "", error: "" };
  const input = inputs.findLast((other) => other.key === key);
  if (input) {
    input.insert(result.text);
    await setVoice(key, () => ready);
  } else {
    await updateDraft(key, (draft) => ({
      ...draft,
      text: insertVoiceText(draft.text, result.text).text,
      voice: ready,
    }));
  }
};

// convertCard converts the card of the draft when it is in the state converting. With no conversion on the server, it
// sends the audio.
const convertCard = async (key: string) => {
  if (converting.has(key)) {
    return;
  }
  converting.add(key);
  const { voice } = await loadDraft(key);
  const result =
    voice?.state === "converting" && voice.audio
      ? await (voice.conversion
          ? awaitConversion(key, voice.conversion, voice.audio)
          : convert(key, voice.audio))
      : undefined;
  converting.delete(key);
  if (result) {
    await settle(key, result);
  }
};

const finish = async (notice: string) => {
  const recording = current;
  if (!recording) {
    return;
  }
  const { key } = recording;
  current = undefined;
  stopping = true;
  clearTimeout(recording.timer);
  clearInterval(recording.live.timer);
  releaseScreen();
  await Promise.all([stopMedia(recording.stream, recording.context), loadDraft(key)]);
  const audio = wav(recording.chunks);
  const stored = updateDraft(key, (draft) => ({
    ...draft,
    voice: { state: "converting", audio, conversion: "", error: "" },
  }));
  stopping = false;
  publish({ recording: false, live: "", notice: notice ? { key, text: notice } : snapshot.notice });
  await stored;
  const conversion = await finishLive(recording);
  if (conversion) {
    await setVoice(key, (voice) => ({ ...voice, conversion }));
  }
  void convertCard(key);
};

export const resend = async (key: string) => {
  await setVoice(key, (voice) =>
    voice.state === "failed" ? { ...voice, state: "converting", conversion: "", error: "" } : voice,
  );
  void convertCard(key);
};

void convertingDraftKeys().then((keys) => keys.forEach((key) => void convertCard(key)));

const start = async (key: string) => {
  const token = ++run;
  const context = new AudioContext();
  void context.resume();
  publish({
    recording: true,
    key,
    startedAt: Date.now(),
    live: "",
    notice: { key: "", text: "" },
  });
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
      publish({ recording: false, notice: { key, text: startError(err) } });
    }
    return;
  }
  if (token !== run) {
    void stopMedia(stream, context);
    return;
  }
  const recording: Recording = {
    key,
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
    },
  };
  current = recording;
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

if (typeof document !== "undefined") {
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "hidden") {
      void finish(backgroundMessage);
    }
  });
}

const cancelStart = () => {
  run++;
  releaseScreen();
  publish({ recording: false });
};

// A stop while the microphone starts cancels the start.
const stopVoice = () => {
  if (current) {
    void finish("");
  } else if (snapshot.recording && !stopping) {
    cancelStart();
  }
};

const toggleVoice = (key: string) => {
  if (snapshot.recording) {
    stopVoice();
  } else {
    void start(key);
  }
};

// The recording stops when no mounted input has its draft key.
const stopWithoutInput = () => {
  if (snapshot.recording && !inputs.some((input) => input.key === snapshot.key)) {
    stopVoice();
  }
};

export const voiceSupported =
  Boolean(typeof navigator !== "undefined" && navigator.mediaDevices?.getUserMedia) &&
  typeof AudioWorkletNode !== "undefined";

export const subscribeVoice = subscribe;
export const getVoiceSnapshot = () => snapshot;
export const toggleVoiceAction = toggleVoice;
export const stopVoiceAction = stopVoice;
export const resendVoiceAction = resend;
export const discardVoiceAction = discard;
export const finishForSendAction = finishForSend;
export const showsLiveVoice = showsLive;
export const registerVoiceInput = (input: {
  insert: (text: string, live: boolean) => void;
  insertLive: (text: string, first: boolean) => boolean;
}) => {
  inputs.push(input);
  return () => {
    const idx = inputs.indexOf(input);
    if (idx !== -1) {
      inputs.splice(idx, 1);
    }
  };
};

// useVoice gives the voice run of the page to the input with the draft key. toggle starts a recording that belongs to
// that key, or stops the recording. During the recording, the server gives the live text about each 2 seconds, and live
// has it for the input of the run. The stop puts the audio in the draft of the key as a card, and the card converts to
// text in the background. The text goes to onText when an input with the key is mounted. The recording stays on until
// toggle or stop stops it, the recording reaches 10 minutes, the microphone stops, or no mounted input has the key. A
// stop while the microphone starts cancels the start. notice is the message of the last start or stop of the key.
export function useVoice(key: string, onText: (text: string) => void) {
  const state = useSyncExternalStore(subscribe, () => snapshot);
  const latestOnText = useRef(onText);

  useEffect(() => {
    latestOnText.current = onText;
  });

  useEffect(() => {
    const input = { key, insert: (text: string) => latestOnText.current(text) };
    inputs.push(input);
    return () => {
      inputs.splice(inputs.indexOf(input), 1);
      queueMicrotask(stopWithoutInput);
    };
  }, [key]);

  const mine = state.recording && state.key === key;
  return {
    supported: voiceSupported,
    recording: state.recording,
    mine,
    startedAt: state.startedAt,
    live: mine ? state.live : "",
    notice: state.notice.key === key ? state.notice.text : "",
    toggle: () => toggleVoice(key),
    stop: stopVoice,
  };
}
