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
  onText: (text: string) => void;
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
  // Chrome on Android adds a final result that repeats the text of the final result before it.
  lastFinal: "",
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

const begin = (live: Recognition) => {
  live.lang = navigator.language;
  state.owner = state.mounted;
  state.added = 0;
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

const create = (Ctor: new () => Recognition) => {
  const live = new Ctor();
  live.continuous = true;
  live.addEventListener("result", (event) => {
    const spoken: string[] = [];
    while (state.added < event.results.length && event.results[state.added].isFinal) {
      const transcript = event.results[state.added][0].transcript.trim();
      if (transcript !== state.lastFinal) {
        spoken.push(
          transcript.startsWith(`${state.lastFinal} `)
            ? transcript.slice(state.lastFinal.length).trim()
            : transcript,
        );
        state.lastFinal = transcript;
      }
      state.added++;
    }
    const text = spoken.join(" ").trim();
    if (text) {
      state.owner?.onText(text);
    }
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
  state.recognition?.abort();
};

// useVoice gives the text of each spoken phrase to onText. It keeps the voice input on until toggle stops it,
// abort runs or an error occurs. It returns the error of the voice input, or '' when the voice input starts or
// abort runs. The unmount stops the voice input.
export function useVoice(onText: (text: string) => void) {
  const [listening, setListening] = useState(false);
  const [error, setError] = useState("");
  const latestOnText = useRef(onText);

  useEffect(() => {
    latestOnText.current = onText;
  });

  useEffect(() => {
    const target = { onText: (text: string) => latestOnText.current(text), setListening, setError };
    state.mounted = target;
    return () => {
      state.mounted = undefined;
      if (state.owner === target) {
        state.owner = undefined;
      }
      abortRun();
    };
  }, []);

  // abort drops the phrase that the run still holds.
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
    state.wanted = true;
    holdScreen();
    setError("");
    setListening(true);
    if (state.running) {
      state.canRestart = true;
    } else {
      begin(state.recognition);
    }
  };

  return { supported: Boolean(Speech), listening, error, toggle, abort };
}
