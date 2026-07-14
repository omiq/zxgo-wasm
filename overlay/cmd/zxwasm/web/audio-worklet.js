// ZX audio worklet: a small Float32 ring buffer fed by the main thread each
// emulator tick (via port messages) and drained here at the AudioContext rate.
// Runs off the main thread, so it keeps playing smoothly through requestAnimationFrame
// jitter. On underrun it holds the last sample (DC) rather than clicking to zero.
class ZXAudioProcessor extends AudioWorkletProcessor {
  constructor() {
    super();
    // ~0.37s of headroom at 44.1kHz. Big enough to absorb the 50Hz producer /
    // ~350Hz consumer mismatch; small enough that latency stays low.
    this.ring = new Float32Array(16384);
    this.head = 0;      // next write index
    this.tail = 0;      // next read index
    this.size = 0;      // samples currently buffered
    this.last = 0;      // held during underrun (no click)
    this.port.onmessage = (e) => this.push(e.data);
  }

  push(samples) {
    for (let i = 0; i < samples.length; i++) {
      if (this.size === this.ring.length) {   // overflow: drop oldest
        this.tail = (this.tail + 1) % this.ring.length;
        this.size--;
      }
      this.ring[this.head] = samples[i];
      this.head = (this.head + 1) % this.ring.length;
      this.size++;
    }
  }

  process(_inputs, outputs) {
    const ch = outputs[0][0];
    for (let i = 0; i < ch.length; i++) {
      if (this.size > 0) {
        this.last = this.ring[this.tail];
        this.tail = (this.tail + 1) % this.ring.length;
        this.size--;
      }
      ch[i] = this.last;   // hold last sample on underrun
    }
    return true;           // keep processor alive
  }
}

registerProcessor('zx-audio', ZXAudioProcessor);
