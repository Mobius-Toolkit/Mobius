declare const sampleRate: number;
declare class AudioWorkletProcessor {
  readonly port: MessagePort;
}
declare function registerProcessor(name: string, processor: new () => AudioWorkletProcessor): void;

const outputRate = 16000;

// Sends the input as mono 16-bit samples at 16 kHz. Each output sample is the mean of the input samples that it covers.
class Recorder extends AudioWorkletProcessor {
  private sum = 0;
  private covered = 0;

  process(inputs: Float32Array[][]) {
    const channels = inputs[0];
    if (channels.length === 0) {
      return true;
    }
    const span = sampleRate / outputRate;
    const output: number[] = [];
    for (let i = 0; i < channels[0].length; i++) {
      let sample = 0;
      for (const channel of channels) {
        sample += channel[i];
      }
      sample /= channels.length;
      let left = 1;
      while (left > 0) {
        const take = Math.min(left, span - this.covered);
        this.sum += sample * take;
        this.covered += take;
        left -= take;
        if (span - this.covered < 1e-9) {
          const mean = Math.max(-1, Math.min(1, this.sum / span));
          output.push(Math.round(mean < 0 ? mean * 0x8000 : mean * 0x7fff));
          this.sum = 0;
          this.covered = 0;
        }
      }
    }
    const samples = Int16Array.from(output);
    this.port.postMessage(samples, [samples.buffer]);
    return true;
  }
}

registerProcessor("recorder", Recorder);
