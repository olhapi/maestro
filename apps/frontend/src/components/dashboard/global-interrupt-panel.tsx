import { useMemo, useState } from "react";

import { Badge } from "@/components/ui/badge";
import type { PendingInterrupt, PendingUserInputQuestion } from "@/lib/types";
import { cn, formatRelativeTimeCompact, toTitleCase } from "@/lib/utils";

const EMPTY_QUESTIONS: PendingUserInputQuestion[] = [];

function answerValue(question: PendingUserInputQuestion, draft: string) {
  if (!question.options?.length) {
    return draft;
  }
  return draft;
}

function interruptSummary(interrupt: PendingInterrupt) {
  if (interrupt.last_activity) {
    return interrupt.last_activity;
  }
  if (interrupt.kind === "approval") {
    return interrupt.approval?.command || interrupt.approval?.reason || "Operator approval required.";
  }
  return interrupt.user_input?.questions?.[0]?.question || "Operator input required.";
}

export function GlobalInterruptPanel({
  current,
  count,
  hiddenCurrentId,
  isSubmitting,
  onRespond,
}: {
  current?: PendingInterrupt;
  count: number;
  hiddenCurrentId?: string | null;
  isSubmitting: boolean;
  onRespond: (payload: {
    decision?: string;
    decision_payload?: Record<string, unknown>;
    answers?: Record<string, string[]>;
  }) => void;
}) {
  const visibleCurrent = current && current.id !== hiddenCurrentId ? current : undefined;
  const visibleCount = visibleCurrent ? count : Math.max(0, count - (hiddenCurrentId ? 1 : 0));
  const [decision, setDecision] = useState("");
  const [draftAnswers, setDraftAnswers] = useState<Record<string, string>>({});
  const questions = visibleCurrent?.user_input?.questions ?? EMPTY_QUESTIONS;
  const answers = useMemo(() => {
    const next: Record<string, string[]> = {};
    for (const question of questions) {
      const value = answerValue(question, draftAnswers[question.id] ?? "");
      if (value.trim()) {
        next[question.id] = [value];
      }
    }
    return next;
  }, [draftAnswers, questions]);

  if (!visibleCurrent || visibleCount === 0) {
    return null;
  }

  const valid =
    visibleCurrent.kind === "approval"
      ? !!decision
      : questions.length > 0 && questions.every((question) => (answers[question.id]?.[0] ?? "").trim().length > 0);

  return (
    <section className="sticky top-[4.75rem] z-20 border-b border-white/10 bg-[rgba(9,12,16,0.94)] backdrop-blur-2xl lg:top-[4.6rem]">
      <div className="mx-auto flex w-full max-w-[1600px] flex-col gap-4 px-[var(--shell-padding)] py-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0 space-y-2">
            <div className="flex flex-wrap items-center gap-2">
              <Badge className="border-amber-400/20 bg-amber-400/10 text-amber-100">
                {visibleCount} waiting
              </Badge>
              {visibleCurrent.collaboration_mode === "plan" ? (
                <Badge className="border-sky-400/20 bg-sky-400/10 text-sky-100">
                  Plan turn
                </Badge>
              ) : null}
              <Badge className="border-white/10 bg-white/5 text-white">
                {visibleCurrent.issue_identifier || "Agent"}
              </Badge>
              {visibleCurrent.phase ? (
                <Badge className="border-white/10 bg-white/5 text-white">
                  {toTitleCase(visibleCurrent.phase)}
                </Badge>
              ) : null}
              {visibleCurrent.attempt ? (
                <Badge className="border-white/10 bg-white/5 text-white">
                  Attempt {visibleCurrent.attempt}
                </Badge>
              ) : null}
            </div>
            <div className="min-w-0">
              <p className="truncate text-sm font-medium text-white">
                {visibleCurrent.issue_title || visibleCurrent.issue_identifier || "Running agent"}
              </p>
              <p className="mt-1 text-sm text-[var(--muted-foreground)]">
                {interruptSummary(visibleCurrent)}
              </p>
              <p className="mt-1 text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                Last activity {formatRelativeTimeCompact(visibleCurrent.last_activity_at || visibleCurrent.requested_at)}
              </p>
            </div>
          </div>
          {visibleCount > 1 ? (
            <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
              {visibleCount - 1} more queued
            </p>
          ) : null}
        </div>

        <form
          className="grid gap-4 rounded-[var(--panel-radius)] border border-white/8 bg-black/25 p-[var(--panel-padding)]"
          onSubmit={(event) => {
            event.preventDefault();
            if (!valid || isSubmitting) {
              return;
            }
            if (visibleCurrent.kind === "approval") {
              const selectedDecision = visibleCurrent.approval?.decisions.find((option) => option.value === decision);
              if (!selectedDecision) {
                return;
              }
              if (selectedDecision.decision_payload) {
                onRespond({ decision_payload: selectedDecision.decision_payload });
                return;
              }
              onRespond({ decision: selectedDecision.value });
              return;
            }
            onRespond({ answers });
          }}
        >
          {visibleCurrent.kind === "approval" ? (
            <div className="grid gap-2 md:grid-cols-2 xl:grid-cols-4">
              {visibleCurrent.approval?.decisions.map((option) => {
                const selected = decision === option.value;
                return (
                  <button
                    key={option.value}
                    className={cn(
                      "rounded-[calc(var(--panel-radius)-0.2rem)] border p-3 text-left transition",
                      selected
                        ? "border-[var(--accent)]/60 bg-[linear-gradient(135deg,rgba(196,255,87,.18),rgba(255,255,255,.05))] text-white"
                        : "border-white/10 bg-white/5 text-[var(--muted-foreground)] hover:border-white/20 hover:text-white",
                    )}
                    type="button"
                    onClick={() => setDecision(option.value)}
                    >
                      <p className="text-sm font-medium">{option.label}</p>
                      {option.description ? (
                      <p className="mt-2 text-xs leading-5 text-[var(--muted-foreground)]">{option.description}</p>
                      ) : null}
                    </button>
                );
              })}
            </div>
          ) : (
            <div className="grid gap-3">
              {questions.map((question) => (
                <label
                  key={question.id}
                  className="grid gap-2 rounded-[calc(var(--panel-radius)-0.2rem)] border border-white/8 bg-white/[0.03] p-3"
                >
                  <div className="space-y-1">
                    {question.header ? (
                      <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                        {question.header}
                      </p>
                    ) : null}
                    <p className="text-sm text-white">{question.question || question.id}</p>
                  </div>
                  {question.options?.length ? (
                    <div className="grid gap-2">
                      {question.options.map((option) => {
                        const checked = draftAnswers[question.id] === option.label;
                        return (
                          <button
                            key={option.label}
                            className={cn(
                              "rounded-xl border px-3 py-2 text-left text-sm transition",
                              checked
                                ? "border-[var(--accent)]/50 bg-[var(--accent)]/10 text-white"
                                : "border-white/10 bg-black/20 text-[var(--muted-foreground)] hover:border-white/20 hover:text-white",
                            )}
                            type="button"
                            onClick={() =>
                              setDraftAnswers((currentDraft) => ({
                                ...currentDraft,
                                [question.id]: option.label,
                              }))
                            }
                          >
                            <span className="font-medium">{option.label}</span>
                            {option.description ? (
                              <span className="ml-2 text-[var(--muted-foreground)]">{option.description}</span>
                            ) : null}
                          </button>
                        );
                      })}
                    </div>
                  ) : null}
                  {!question.options?.length || question.is_other ? (
                    <input
                      className="h-11 rounded-xl border border-white/10 bg-black/20 px-3 text-sm text-white outline-none transition focus:border-[var(--accent)]/50"
                      placeholder={question.is_secret ? "Enter response" : "Type response"}
                      type={question.is_secret ? "password" : "text"}
                      value={draftAnswers[question.id] ?? ""}
                      onChange={(event) =>
                        setDraftAnswers((currentDraft) => ({
                          ...currentDraft,
                          [question.id]: event.target.value,
                        }))
                      }
                    />
                  ) : null}
                </label>
              ))}
            </div>
          )}

          <div className="flex items-center justify-end gap-3">
            <button
              className={cn(
                "inline-flex h-11 items-center rounded-2xl border px-4 text-sm font-medium transition",
                valid && !isSubmitting
                  ? "border-[var(--accent)]/45 bg-[linear-gradient(135deg,rgba(196,255,87,.24),rgba(255,255,255,.06))] text-white hover:border-[var(--accent)]/60"
                  : "border-white/10 bg-white/5 text-white/45",
              )}
              disabled={!valid || isSubmitting}
              type="submit"
            >
              {isSubmitting ? "Submitting..." : "Submit response"}
            </button>
          </div>
        </form>
      </div>
    </section>
  );
}
