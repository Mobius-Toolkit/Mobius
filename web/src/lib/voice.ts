import { useEffect, useRef, useState } from "react";

// The DOM types have the events of the Web Speech API, but not the recognition. Chrome and Safari have only
// webkitSpeechRecognition.
type Recognition = {
  lang: string;
  continuous: boolean;
  start(): void;
  stop(): void;
  abort(): void;
  addEventListener(type: "result", listener: (event: SpeechRecognitionEvent) => void): void;
  addEventListener(type: "error", listener: (event: SpeechRecognitionErrorEvent) => void): void;
  addEventListener(type: "audiostart" | "end", listener: () => void): void;
};

const speech = window as Window & {
  SpeechRecognition?: new () => Recognition;
  webkitSpeechRecognition?: new () => Recognition;
};

const Speech = speech.SpeechRecognition ?? speech.webkitSpeechRecognition;

const errorMessages: Record<string, string> = {
  "not-allowed": "The browser blocks the microphone. Allow the microphone in the browser settings.",
  "service-not-allowed":
    "The speech service of the browser or of the device is not available. Turn on Siri or Dictation, or use a different browser.",
  "no-speech": "The microphone did not hear speech. Speak again.",
  "audio-capture": "The browser cannot find a microphone. Connect a microphone and try again.",
  network: "The speech service has no network connection. Check the connection and try again.",
  "language-not-supported": "The speech service does not support this language.",
};

// The hook that receives the events of the run.
type Target = {
  onText: (text: string, first: boolean, detach: () => string) => void;
  setListening: (listening: boolean) => void;
  setError: (error: string) => void;
};

// WebKit allows one start() for each end event, and a recognition object that a new page part makes can fail
// silently. Thus the page has one recognition object, and each start waits for the end event of the run before it.
// The state of the object and of its run does not belong to one mount of useVoice.
const state = {
  recognition: undefined as Recognition | undefined,
  mounted: undefined as Target | undefined,
  // The hook that started the run. An unmount clears it, so the events of the run go nowhere.
  owner: undefined as Target | undefined,
  wanted: false,
  running: false,
  // A run that never captured audio does not restart after its end event. This stops a loop of failed runs.
  canRestart: false,
  // The result list of a run has all final results of the run. The new run starts a new list.
  added: 0,
  // True after a new recording starts while the old run still runs. The results of the old run belong to the old
  // recording.
  stale: false,
  // Chrome on Android adds a final result that repeats the text of the final result before it.
  lastFinal: "",
  // The voice text of one recording is the final texts of all its runs and the draft of the last run.
  finals: [] as string[],
  // The interim text that no final result has replaced.
  draft: "",
  // The words at the start of the draft that the field shows in an older place. A touch of the field sets them.
  skip: 0,
  // What the field shows: the number of final texts, and the number of words of the draft that is not skipped.
  shownFinals: 0,
  shownWords: 0,
  // The voice text that the owner got last.
  spoken: "",
  // True until the owner gets the first voice text of the recording.
  first: true,
};

// The lock that keeps the screen on while the voice input listens.
let screenLock: WakeLockSentinel | undefined;

// WebKit grants the lock only after a recent tap while the permission state is prompt.
// Thus the tap on the voice button requests the lock.
const holdScreen = () => {
  navigator.wakeLock?.request("screen").then(
    (lock) => {
      if (!state.wanted) {
        void lock.release();
        return;
      }
      void screenLock?.release();
      screenLock = lock;
    },
    () => {},
  );
};

const stopWanting = () => {
  state.wanted = false;
  void screenLock?.release();
  screenLock = undefined;
};

// The browser releases the lock when the page is hidden.
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible" && state.wanted) {
    holdScreen();
  }
});

const visibleDraft = () => state.draft.split(" ").slice(state.skip).join(" ");

const voiceText = () => [...state.finals, visibleDraft()].filter(Boolean).join(" ");

const begin = (live: Recognition) => {
  live.lang = navigator.language;
  state.owner = state.mounted;
  state.added = 0;
  state.stale = false;
  state.lastFinal = "";
  state.canRestart = false;
  try {
    live.start();
  } catch {
    stopWanting();
    state.owner?.setListening(false);
    state.owner?.setError("The voice input did not start.");
    return;
  }
  state.running = true;
};

// After the user touches the field, the voice text starts again at the touched place. The field already shows the
// final texts and the draft words that it got, so the new voice text has only the words after them.
const detach = () => {
  state.finals = state.finals.slice(state.shownFinals);
  if (state.finals.length > 0) {
    state.finals[0] = state.finals[0].split(" ").slice(state.shownWords).join(" ");
    state.skip = 0;
  } else if (state.draft) {
    state.skip += state.shownWords;
  } else {
    state.skip = 0;
  }
  state.spoken = voiceText();
  return state.spoken;
};

const create = (Ctor: new () => Recognition) => {
  const live = new Ctor();
  live.continuous = true;
  live.addEventListener("result", (event) => {
    if (state.stale) {
      return;
    }
    while (state.added < event.results.length && event.results[state.added].isFinal) {
      const transcript = event.results[state.added][0].transcript.trim();
      if (transcript !== state.lastFinal) {
        const piece = transcript.startsWith(`${state.lastFinal} `)
          ? transcript.slice(state.lastFinal.length).trim()
          : transcript;
        const fresh = piece.split(" ").slice(state.skip).join(" ");
        if (fresh) {
          state.finals.push(fresh);
        }
        state.lastFinal = transcript;
      }
      state.skip = 0;
      state.added++;
    }
    state.draft = Array.from(event.results)
      .slice(state.added)
      .map((part) => part[0].transcript.trim())
      .join(" ")
      .trim();
    const text = voiceText();
    if (text !== state.spoken) {
      state.spoken = text;
      state.owner?.onText(text, state.first, detach);
      state.first = false;
    }
    state.shownFinals = state.finals.length;
    state.shownWords = visibleDraft() === "" ? 0 : visibleDraft().split(" ").length;
  });
  live.addEventListener("audiostart", () => {
    state.canRestart = true;
  });
  live.addEventListener("error", (event) => {
    if (!state.owner || !state.wanted || event.error === "aborted") {
      return;
    }
    stopWanting();
    state.owner.setListening(false);
    state.owner.setError(errorMessages[event.error] ?? `The voice input failed: ${event.error}`);
  });
  live.addEventListener("end", () => {
    state.running = false;
    const rest = visibleDraft();
    if (rest) {
      state.finals.push(rest);
    }
    state.draft = "";
    state.skip = 0;
    state.shownFinals = state.finals.length;
    state.shownWords = 0;
    if (state.wanted && state.canRestart) {
      begin(live);
      return;
    }
    stopWanting();
    state.owner?.setListening(false);
  });
  return live;
};

const abortRun = () => {
  stopWanting();
  state.owner = undefined;
  state.recognition?.abort();
};

// useVoice gives the voice text of the recording to onText after each result. The voice text has the final texts and
// the draft. first is true for the first voice text of a recording. When a run ends, its draft becomes a final text.
// The voice input stays on until toggle stops it, abort runs or an error occurs. useVoice returns the error of the
// voice input, or '' when the voice input starts or abort runs. The unmount stops the voice input. onText calls its
// third argument when the user has touched the field. That call gives the voice text that the field does not show.
export function useVoice(onText: (text: string, first: boolean, detach: () => string) => void) {
  const [listening, setListening] = useState(false);
  const [error, setError] = useState("");
  const latestOnText = useRef(onText);

  useEffect(() => {
    latestOnText.current = onText;
  });

  useEffect(() => {
    const target: Target = {
      onText: (text, first, cut) => latestOnText.current(text, first, cut),
      setListening,
      setError,
    };
    state.mounted = target;
    return () => {
      state.mounted = undefined;
      abortRun();
    };
  }, []);

  // abort drops the voice text that the run still holds.
  const abort = () => {
    setListening(false);
    setError("");
    abortRun();
  };

  const toggle = () => {
    if (state.wanted) {
      stopWanting();
      setListening(false);
      state.recognition?.stop();
      return;
    }
    if (!Speech) {
      return;
    }
    state.recognition ??= create(Speech);
    state.finals = [];
    state.draft = "";
    state.spoken = "";
    state.skip = 0;
    state.shownFinals = 0;
    state.shownWords = 0;
    state.first = true;
    state.wanted = true;
    holdScreen();
    setError("");
    setListening(true);
    if (state.running) {
      state.stale = true;
      state.canRestart = true;
    } else {
      begin(state.recognition);
    }
  };

  return { supported: Boolean(Speech), listening, error, toggle, abort };
}
