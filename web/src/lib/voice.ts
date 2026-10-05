import { useCallback, useEffect, useRef, useState } from 'react'

// The DOM types have the events of the Web Speech API, but not the recognition. Chrome and Safari have only
// webkitSpeechRecognition.
type Recognition = {
  lang: string
  start(): void
  stop(): void
  abort(): void
  addEventListener(
    type: 'result',
    listener: (event: SpeechRecognitionEvent) => void,
  ): void
  addEventListener(
    type: 'error',
    listener: (event: SpeechRecognitionErrorEvent) => void,
  ): void
  addEventListener(type: 'end', listener: () => void): void
}

const speech = window as Window & {
  SpeechRecognition?: new () => Recognition
  webkitSpeechRecognition?: new () => Recognition
}

// useVoice gives the text of one spoken phrase to onText. setError gets the error of the voice input, or '' when
// the voice input starts.
export function useVoice(
  onText: (text: string) => void,
  setError: (error: string) => void,
) {
  const recognition = useRef<Recognition>(undefined)
  // The sessions that can still send a result. A session that "Stop mic" stopped stays here until its end event.
  const sessions = useRef(new Set<Recognition>())
  const [listening, setListening] = useState(false)

  // abort drops the phrase that each session still holds.
  const abort = useCallback(() => {
    recognition.current = undefined
    setListening(false)
    for (const live of sessions.current) {
      live.abort()
    }
    sessions.current.clear()
  }, [])

  useEffect(() => abort, [abort])

  const toggle = () => {
    if (recognition.current) {
      const live = recognition.current
      recognition.current = undefined
      setListening(false)
      live.stop()
      return
    }
    const Speech = speech.SpeechRecognition ?? speech.webkitSpeechRecognition
    if (!Speech) {
      setError('This browser has no voice input.')
      return
    }
    const live = new Speech()
    live.lang = navigator.language
    let added = 0
    live.addEventListener('result', (event) => {
      const spoken: string[] = []
      while (added < event.results.length && event.results[added].isFinal) {
        spoken.push(event.results[added][0].transcript)
        added++
      }
      const text = spoken.join(' ').trim()
      if (text) {
        onText(text)
      }
    })
    live.addEventListener('error', (event) => {
      if (recognition.current !== live) {
        return
      }
      if (
        event.error === 'not-allowed' ||
        event.error === 'service-not-allowed'
      ) {
        setError(
          'The browser blocks the microphone. Allow the microphone in the browser settings.',
        )
      } else if (event.error !== 'aborted') {
        setError(`The voice input failed: ${event.error}`)
      }
    })
    live.addEventListener('end', () => {
      sessions.current.delete(live)
      if (recognition.current === live) {
        recognition.current = undefined
        setListening(false)
      }
    })
    try {
      live.start()
    } catch {
      setError('The voice input did not start.')
      return
    }
    recognition.current = live
    sessions.current.add(live)
    setError('')
    setListening(true)
  }

  return { listening, toggle, abort }
}
