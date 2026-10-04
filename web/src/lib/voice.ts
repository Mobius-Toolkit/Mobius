import { useEffect, useRef, useState } from 'react'

// The DOM types have the events of the Web Speech API, but not the recognition. Chrome and Safari have only
// webkitSpeechRecognition.
type Recognition = {
  lang: string
  start(): void
  stop(): void
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
  const [listening, setListening] = useState(false)

  useEffect(() => () => recognition.current?.stop(), [])

  const toggle = () => {
    if (recognition.current) {
      recognition.current.stop()
      return
    }
    const Speech = speech.SpeechRecognition ?? speech.webkitSpeechRecognition
    if (!Speech) {
      setError('This browser has no voice input.')
      return
    }
    const live = new Speech()
    live.lang = 'en-US'
    live.addEventListener('result', (event) =>
      onText(
        Array.from(event.results, (result) => result[0].transcript)
          .join(' ')
          .trim(),
      ),
    )
    live.addEventListener('error', (event) => {
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
      recognition.current = undefined
      setListening(false)
    })
    live.start()
    recognition.current = live
    setError('')
    setListening(true)
  }

  return { listening, toggle }
}
