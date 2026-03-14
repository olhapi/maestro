import { Mic, Square } from "lucide-react";
import { useEffect, useId, useMemo, useRef, useState } from "react";

import { Textarea } from "@/components/ui/textarea";

type SpeechState = "unsupported" | "idle" | "starting" | "listening";

function resolveSpeechRecognition() {
  if (typeof window === "undefined") {
    return undefined;
  }
  return window.SpeechRecognition ?? window.webkitSpeechRecognition;
}

function appendTranscript(baseValue: string, transcript: string) {
  const normalizedTranscript = transcript.trim();
  if (!normalizedTranscript) {
    return baseValue;
  }
  if (!baseValue.trim()) {
    return normalizedTranscript;
  }
  const separator = /\s$/.test(baseValue) ? "" : " ";
  return `${baseValue}${separator}${normalizedTranscript}`;
}

function joinSessionTranscript(finalTranscript: string, interimTranscript: string) {
  const parts = [finalTranscript.trim(), interimTranscript.trim()].filter(Boolean);
  return parts.join(" ");
}

function getSpeechErrorMessage(error: string) {
  switch (error) {
    case "not-allowed":
    case "service-not-allowed":
      return "Microphone access was denied. Allow microphone access and try again.";
    case "audio-capture":
      return "No microphone was found for browser speech input.";
    case "network":
      return "Browser speech input hit a network error. Try again.";
    case "language-not-supported":
      return "This browser cannot recognize speech for the current language.";
    case "no-speech":
      return "No speech was detected. Try again when you are ready.";
    default:
      return "Browser speech input could not start. Try again.";
  }
}

export function IssueDescriptionField({
  labelledBy,
  value,
  onChange,
  disabled,
}: {
  labelledBy: string;
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
}) {
  const descriptionStatusId = useId();
  const recognitionRef = useRef<SpeechRecognition | null>(null);
  const baseValueRef = useRef(value);
  const activityTimeoutRef = useRef<number | null>(null);
  const [sessionBaseValue, setSessionBaseValue] = useState(value);
  const [speechState, setSpeechState] = useState<SpeechState>(
    resolveSpeechRecognition() ? "idle" : "unsupported",
  );
  const [finalTranscript, setFinalTranscript] = useState("");
  const [interimTranscript, setInterimTranscript] = useState("");
  const [statusMessage, setStatusMessage] = useState("");
  const [hasRecentActivity, setHasRecentActivity] = useState(false);

  useEffect(() => {
    return () => {
      if (activityTimeoutRef.current !== null) {
        window.clearTimeout(activityTimeoutRef.current);
      }
      recognitionRef.current?.abort();
      recognitionRef.current = null;
    };
  }, []);

  const isListening = speechState === "starting" || speechState === "listening";
  const displayValue = useMemo(() => {
    if (!isListening) {
      return value;
    }
    return appendTranscript(sessionBaseValue, joinSessionTranscript(finalTranscript, interimTranscript));
  }, [finalTranscript, interimTranscript, isListening, sessionBaseValue, value]);
  const supportsSpeechInput = speechState !== "unsupported";

  const signalSpeechActivity = () => {
    setHasRecentActivity(true);
    if (activityTimeoutRef.current !== null) {
      window.clearTimeout(activityTimeoutRef.current);
    }
    activityTimeoutRef.current = window.setTimeout(() => {
      setHasRecentActivity(false);
      activityTimeoutRef.current = null;
    }, 320);
  };

  const stopRecognition = () => {
    recognitionRef.current?.stop();
  };

  const startRecognition = () => {
    const SpeechRecognitionConstructor = resolveSpeechRecognition();
    if (!SpeechRecognitionConstructor) {
      setSpeechState("unsupported");
      return;
    }

    recognitionRef.current?.abort();
    recognitionRef.current = null;
    baseValueRef.current = value;
    setSessionBaseValue(value);
    setFinalTranscript("");
    setInterimTranscript("");
    setHasRecentActivity(false);
    setStatusMessage("");
    setSpeechState("starting");

    const recognition = new SpeechRecognitionConstructor();
    recognition.continuous = true;
    recognition.interimResults = true;
    recognition.lang = navigator.language || "en-US";

    recognition.onstart = () => {
      setSpeechState("listening");
    };

    recognition.onresult = (event) => {
      let nextFinalTranscript = "";
      let nextInterimTranscript = "";

      for (let index = 0; index < event.results.length; index += 1) {
        const result = event.results[index];
        const transcript = result[0]?.transcript ?? "";
        if (result.isFinal) {
          nextFinalTranscript += transcript;
        } else {
          nextInterimTranscript += transcript;
        }
      }

      setFinalTranscript(nextFinalTranscript);
      setInterimTranscript(nextInterimTranscript);
      onChange(appendTranscript(baseValueRef.current, nextFinalTranscript));
      signalSpeechActivity();
    };

    recognition.onerror = (event) => {
      setStatusMessage(getSpeechErrorMessage(event.error));
      setSpeechState("idle");
      setFinalTranscript("");
      setInterimTranscript("");
      setHasRecentActivity(false);
    };

    recognition.onend = () => {
      recognitionRef.current = null;
      setSpeechState((current) => (current === "unsupported" ? current : "idle"));
      setInterimTranscript("");
      setFinalTranscript("");
      setHasRecentActivity(false);
      if (activityTimeoutRef.current !== null) {
        window.clearTimeout(activityTimeoutRef.current);
        activityTimeoutRef.current = null;
      }
    };

    recognitionRef.current = recognition;

    try {
      recognition.start();
    } catch (error) {
      recognitionRef.current = null;
      setSpeechState("idle");
      setStatusMessage(
        error instanceof Error && error.message
          ? error.message
          : "Browser speech input could not start. Try again.",
      );
    }
  };

  return (
    <div className="grid gap-3">
      <div className="relative">
        {supportsSpeechInput ? (
          <div className="pointer-events-none absolute right-3 top-3 z-10 flex items-center gap-2 rounded-full border border-white/10 bg-black/75 px-2.5 py-1.5 shadow-[0_10px_24px_rgba(0,0,0,0.28)] backdrop-blur-sm">
            {isListening ? (
              <div
                aria-hidden="true"
                data-testid="issue-speech-visualizer"
                className="issue-speech-visualizer"
                data-speaking={hasRecentActivity ? "true" : "false"}
              >
                <span />
                <span />
                <span />
                <span />
              </div>
            ) : null}
            <button
              type="button"
              aria-label={isListening ? "Stop speech to text" : "Start speech to text"}
              className="pointer-events-auto inline-flex size-8 items-center justify-center rounded-full border border-white/10 bg-white/5 text-white transition hover:bg-white/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent)] disabled:cursor-not-allowed disabled:opacity-40"
              disabled={disabled}
              onClick={isListening ? stopRecognition : startRecognition}
            >
              {isListening ? <Square className="size-3.5" /> : <Mic className="size-4" />}
            </button>
          </div>
        ) : null}
        <Textarea
          aria-describedby={statusMessage ? descriptionStatusId : undefined}
          aria-labelledby={labelledBy}
          value={displayValue}
          onChange={(event) => {
            if (!isListening) {
              onChange(event.target.value);
            }
          }}
          readOnly={isListening}
          className={supportsSpeechInput ? "min-h-[180px] pr-24" : "min-h-[180px]"}
        />
      </div>
      {statusMessage ? (
        <p
          id={descriptionStatusId}
          className="text-xs leading-5 text-amber-200"
        >
          {statusMessage}
        </p>
      ) : null}
    </div>
  );
}
