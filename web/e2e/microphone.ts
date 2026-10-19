// The fake microphone gives a tone from an oscillator. It keeps each stream in microphones. A test sets
// microphoneError to the name of the error that getUserMedia throws. A test sets microphoneGate to a promise to hold
// the answer of getUserMedia.
export const fakeMicrophone = `window.microphones = []
  navigator.mediaDevices.getUserMedia = async () => {
    await window.microphoneGate
    if (window.microphoneError) throw new DOMException('', window.microphoneError)
    const context = new AudioContext()
    void context.resume()
    const oscillator = context.createOscillator()
    const destination = context.createMediaStreamDestination()
    oscillator.connect(destination)
    oscillator.start()
    window.microphones.push(destination.stream)
    return destination.stream
  }`;

// The fake wake lock keeps each lock in locks. A lock has released true after its release().
export const fakeWakeLock = `window.locks = []
  Object.defineProperty(navigator, 'wakeLock', { configurable: true, value: {
    request: async (type) => {
      if (window.wakeLockError) throw new Error(window.wakeLockError)
      const lock = Object.assign(new EventTarget(), { type, released: false })
      lock.release = async () => {
        if (lock.released) return
        lock.released = true
        lock.dispatchEvent(new Event('release'))
      }
      window.locks.push(lock)
      return lock
    },
  } })`;

// The count of the sample messages that the recorder got. A recording that stops before the first message has no
// audio.
export const countSamples = `window.samples = 0
  const Recorder = AudioWorkletNode
  window.AudioWorkletNode = class extends Recorder {
    constructor(...args) {
      super(...args)
      this.port.addEventListener('message', () => { window.samples++ })
    }
  }`;
