export class MockSpeechRecognition {
  static instances: MockSpeechRecognition[] = [];

  continuous = false;
  interimResults = false;
  lang = "";
  onend: ((event: Event) => void) | null = null;
  onerror: ((event: SpeechRecognitionErrorEvent) => void) | null = null;
  onresult: ((event: SpeechRecognitionEvent) => void) | null = null;
  onstart: ((event: Event) => void) | null = null;
  started = false;

  constructor() {
    MockSpeechRecognition.instances.push(this);
  }

  start() {
    this.started = true;
    this.onstart?.(new Event("start"));
  }

  stop() {
    this.started = false;
    this.onend?.(new Event("end"));
  }

  abort() {
    this.started = false;
    this.onend?.(new Event("end"));
  }

  emitResult(parts: Array<{ transcript: string; isFinal: boolean }>) {
    const results = parts.map((part) => ({
      0: { transcript: part.transcript, confidence: 0.99 },
      isFinal: part.isFinal,
      length: 1,
    })) as unknown as SpeechRecognitionResultList;

    this.onresult?.({
      resultIndex: 0,
      results,
    } as SpeechRecognitionEvent);
  }

  emitError(error: string, message = "") {
    this.onerror?.({
      error,
      message,
    } as SpeechRecognitionErrorEvent);
  }

  static reset() {
    MockSpeechRecognition.instances = [];
  }
}
