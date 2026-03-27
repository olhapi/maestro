type AudioContextConstructor = typeof AudioContext
type InterruptAudioWindow = Window & typeof globalThis & { webkitAudioContext?: AudioContextConstructor }

const interruptChime = {
  attackGain: 0.08,
  attackTimeSeconds: 0.012,
  decayGain: 0.0001,
  decayTimeSeconds: 0.29,
  dipGain: 0.03,
  dipTimeSeconds: 0.08,
  firstNoteHz: 783.99,
  initialGain: 0.0001,
  secondAccentGain: 0.085,
  secondAccentTimeSeconds: 0.095,
  secondNoteHz: 1046.5,
  secondNoteTimeSeconds: 0.085,
  stopTimeSeconds: 0.3,
} as const

export function playInterruptNotificationChime() {
  if (typeof window === 'undefined') {
    return
  }

  const AudioContextImpl =
    (window as InterruptAudioWindow).AudioContext ?? (window as InterruptAudioWindow).webkitAudioContext
  if (!AudioContextImpl) {
    return
  }

  try {
    const context = new AudioContextImpl()
    const now = context.currentTime
    const oscillator = context.createOscillator()
    const gain = context.createGain()
    let closed = false
    const closeContext = () => {
      if (closed) {
        return
      }
      closed = true
      void context.close().catch(() => {})
    }

    oscillator.type = 'triangle'
    oscillator.frequency.setValueAtTime(interruptChime.firstNoteHz, now)
    // Step between notes so the interrupt reads as a chime instead of a sweep.
    oscillator.frequency.setValueAtTime(interruptChime.secondNoteHz, now + interruptChime.secondNoteTimeSeconds)

    gain.gain.setValueAtTime(interruptChime.initialGain, now)
    gain.gain.linearRampToValueAtTime(interruptChime.attackGain, now + interruptChime.attackTimeSeconds)
    gain.gain.linearRampToValueAtTime(interruptChime.dipGain, now + interruptChime.dipTimeSeconds)
    gain.gain.linearRampToValueAtTime(interruptChime.secondAccentGain, now + interruptChime.secondAccentTimeSeconds)
    gain.gain.linearRampToValueAtTime(interruptChime.decayGain, now + interruptChime.decayTimeSeconds)

    oscillator.connect(gain)
    gain.connect(context.destination)

    void context.resume().catch(() => {
      closeContext()
    })
    oscillator.start(now)
    oscillator.stop(now + interruptChime.stopTimeSeconds)
    oscillator.addEventListener(
      'ended',
      () => {
        closeContext()
      },
      { once: true },
    )
  } catch {
    // Ignore audio failures so interrupts still render even when autoplay is blocked.
  }
}
