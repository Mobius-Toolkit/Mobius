import { useCallback, useEffect, useRef, useState } from "react";

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

// useVoice gives the text of each spoken phrase to onText. It keeps the voice input on until toggle stops it,
// abort runs or an error occurs. It returns the error of the voice input, or '' when the voice input starts or
// abort runs.
export function useVoice(onText: (text: string) => void) {
  // WebKit allows one start() for each end event. A start() before the end event throws InvalidStateError.
  // Thus one object does all starts, and each start waits for the end event of the run before it.
  const recognition = useRef<Recognition>(undefined);
  const wanted = useRef(false);
  const running = useRef(false);
  // A run that never captured audio does not restart after its end event. This stops a loop of failed runs.
  const canRestart = useRef(false);
  // The result list of a run has all final results of the run. The new run starts a new list.
  const added = useRef(0);
  const [listening, setListening] = useState(false);
  const [error, setError] = useState("");

  const begin = (live: Recognition) => {
    live.lang = navigator.language;
    added.current = 0;
    canRestart.current = false;
    try {
      live.start();
    } catch {
      wanted.current = false;
      setListening(false);
      setError("The voice input did not start.");
      return;
    }
    running.current = true;
  };

  const create = (Ctor: new () => Recognition) => {
    const live = new Ctor();
    live.continuous = true;
    live.addEventListener("result", (event) => {
      const spoken: string[] = [];
      while (added.current < event.results.length && event.results[added.current].isFinal) {
        spoken.push(event.results[added.current][0].transcript);
        added.current++;
      }
      const text = spoken.join(" ").trim();
      if (text) {
        onText(text);
      }
    });
    live.addEventListener("audiostart", () => {
      canRestart.current = true;
    });
    live.addEventListener("error", (event) => {
      if (!wanted.current || event.error === "aborted") {
        return;
      }
      wanted.current = false;
      setListening(false);
      setError(errorMessages[event.error] ?? `The voice input failed: ${event.error}`);
    });
    live.addEventListener("end", () => {
      running.current = false;
      if (wanted.current && canRestart.current) {
        begin(live);
        return;
      }
      wanted.current = false;
      setListening(false);
    });
    return live;
  };

  // abort drops the phrase that the run still holds.
  const abort = useCallback(() => {
    wanted.current = false;
    setListening(false);
    setError("");
    recognition.current?.abort();
  }, []);

  useEffect(() => abort, [abort]);

  const toggle = () => {
    if (wanted.current) {
      wanted.current = false;
      setListening(false);
      recognition.current?.stop();
      return;
    }
    if (!Speech) {
      return;
    }
    recognition.current ??= create(Speech);
    wanted.current = true;
    setError("");
    setListening(true);
    if (running.current) {
      canRestart.current = true;
    } else {
      begin(recognition.current);
    }
  };

  return { supported: Boolean(Speech), listening, error, toggle, abort };
}
