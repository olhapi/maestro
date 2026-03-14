import React from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useEffect, useMemo, useRef, useState } from "react";

export interface SearchEntry {
  title: string;
  href: string;
  description: string;
  section: string;
  searchText?: string;
}

const TRIGGER_SELECTOR = "[data-search-trigger]";

function normalizeQuery(value: string) {
  return value.trim().toLowerCase();
}

function getEntrySearchText(entry: SearchEntry) {
  return normalizeQuery(entry.searchText ?? `${entry.title}\n${entry.description}\n${entry.section}`);
}

function scoreEntry(entry: SearchEntry, query: string) {
  const title = normalizeQuery(entry.title);
  const description = normalizeQuery(entry.description);
  const section = normalizeQuery(entry.section);
  const searchText = getEntrySearchText(entry);
  const terms = query.split(/\s+/u).filter(Boolean);

  if (!query) return 0;

  const hasWholeQuery =
    title.includes(query) || description.includes(query) || section.includes(query) || searchText.includes(query);
  const allTermsMatch = terms.every((term) => searchText.includes(term));

  if (!hasWholeQuery && !allTermsMatch) {
    return 0;
  }

  let score = 0;

  if (title === query) score += 1_000;
  if (title.startsWith(query)) score += 700;
  if (title.includes(query)) score += 450;
  if (description.includes(query)) score += 220;
  if (section.includes(query)) score += 120;
  if (searchText.includes(query)) score += 80;
  if (terms.length > 1 && terms.every((term) => title.includes(term))) score += 180;

  for (const term of terms) {
    if (title.includes(term)) score += 90;
    if (description.includes(term)) score += 30;
    if (section.includes(term)) score += 20;
    if (searchText.includes(term)) score += 15;
  }

  return score;
}

function isTypingTarget(target: EventTarget | null) {
  if (!(target instanceof HTMLElement)) return false;
  const tagName = target.tagName.toLowerCase();
  return tagName === "input" || tagName === "textarea" || target.isContentEditable;
}

export default function SearchOverlay({ entries }: { entries: SearchEntry[] }) {
  const reducedMotion = useReducedMotion() ?? false;
  const inputRef = useRef<HTMLInputElement>(null);
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [selectedIndex, setSelectedIndex] = useState(0);

  const filteredEntries = useMemo(() => {
    const value = normalizeQuery(query);
    if (!value) return entries;
    return entries
      .map((entry, index) => ({
        entry,
        index,
        score: scoreEntry(entry, value),
      }))
      .filter((candidate) => candidate.score > 0)
      .sort((left, right) => right.score - left.score || left.index - right.index)
      .map((candidate) => candidate.entry);
  }, [entries, query]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setOpen((value) => !value);
        return;
      }

      if (!open && event.key === "/" && !isTypingTarget(event.target)) {
        event.preventDefault();
        setOpen(true);
        return;
      }

      if (!open) return;

      if (event.key === "Escape") {
        event.preventDefault();
        setOpen(false);
        return;
      }

      if (event.key === "ArrowDown") {
        event.preventDefault();
        setSelectedIndex((index) => Math.min(index + 1, Math.max(filteredEntries.length - 1, 0)));
        return;
      }

      if (event.key === "ArrowUp") {
        event.preventDefault();
        setSelectedIndex((index) => Math.max(index - 1, 0));
        return;
      }

      if (event.key === "Enter" && filteredEntries[selectedIndex]) {
        event.preventDefault();
        navigate(filteredEntries[selectedIndex].href);
      }
    };

    const onTrigger = (event: Event) => {
      event.preventDefault();
      setOpen(true);
    };

    window.addEventListener("keydown", onKeyDown);
    const triggers = Array.from(document.querySelectorAll<HTMLElement>(TRIGGER_SELECTOR));
    triggers.forEach((trigger) => trigger.addEventListener("click", onTrigger));

    return () => {
      window.removeEventListener("keydown", onKeyDown);
      triggers.forEach((trigger) => trigger.removeEventListener("click", onTrigger));
    };
  }, [filteredEntries, open, selectedIndex]);

  useEffect(() => {
    if (!open) return;
    setSelectedIndex(0);
    const handle = window.setTimeout(() => {
      inputRef.current?.focus();
    }, 0);

    return () => window.clearTimeout(handle);
  }, [open]);

  function navigate(href: string) {
    setOpen(false);
    setQuery("");
    window.location.assign(href);
  }

  return (
    <AnimatePresence>
      {open ? (
        <motion.div
          animate={{ opacity: 1 }}
          className="fixed inset-0 z-[80] bg-black/65 px-4 py-10 backdrop-blur-xl"
          exit={{ opacity: 0 }}
          initial={{ opacity: 0 }}
          onClick={() => setOpen(false)}
        >
          <motion.div
            animate={{ opacity: 1, y: 0, scale: 1 }}
            aria-modal="true"
            className="panel mx-auto w-full max-w-3xl overflow-hidden"
            exit={reducedMotion ? { opacity: 1 } : { opacity: 0, y: 12, scale: 0.98 }}
            initial={reducedMotion ? { opacity: 1, y: 0, scale: 1 } : { opacity: 0, y: 18, scale: 0.98 }}
            onClick={(event) => event.stopPropagation()}
            role="dialog"
            transition={reducedMotion ? { duration: 0 } : { duration: 0.2, ease: "easeOut" }}
          >
            <div className="border-b border-white/8 px-5 py-4">
              <div className="flex items-center gap-3">
                <div className="hidden rounded-full border border-[rgba(196,255,87,0.2)] bg-[rgba(196,255,87,0.08)] px-3 py-1 text-xs uppercase tracking-[0.16em] text-[var(--accent-strong)] sm:inline-flex">
                  Search
                </div>
                <span className="text-sm text-[var(--muted)]">Jump across docs, quickstart, and the tour with Ctrl/Cmd + K.</span>
              </div>
              <input
                aria-label="Search docs"
                className="mt-4 w-full rounded-[1rem] border border-white/8 bg-[rgba(0,0,0,0.35)] px-4 py-3 text-base text-white outline-none placeholder:text-[var(--muted)] focus:border-[rgba(196,255,87,0.3)]"
                onChange={(event) => {
                  setQuery(event.target.value);
                  setSelectedIndex(0);
                }}
                placeholder="Search install, workflow config, operations, or control center"
                ref={inputRef}
                type="search"
                value={query}
              />
            </div>
            <div className="max-h-[26rem] overflow-y-auto px-3 py-3">
              {filteredEntries.length > 0 ? (
                filteredEntries.map((entry, index) => (
                  <button
                    className={`mb-2 block w-full rounded-[1rem] border px-4 py-3 text-left transition ${
                      index === selectedIndex
                        ? "border-[rgba(196,255,87,0.3)] bg-[rgba(196,255,87,0.1)]"
                        : "border-transparent bg-[rgba(255,255,255,0.02)] hover:border-white/10 hover:bg-white/5"
                    }`}
                    key={`${entry.href}-${entry.title}`}
                    onClick={() => navigate(entry.href)}
                    type="button"
                  >
                    <div className="flex items-center justify-between gap-3">
                      <span className="font-medium text-white">{entry.title}</span>
                      <span className="rounded-full border border-white/10 px-2.5 py-0.5 text-[0.68rem] uppercase tracking-[0.16em] text-[var(--muted)]">
                        {entry.section}
                      </span>
                    </div>
                    <p className="mt-2 text-sm leading-6 text-[var(--muted)]">{entry.description}</p>
                  </button>
                ))
              ) : (
                <div className="rounded-[1rem] border border-white/8 bg-black/25 px-4 py-6 text-sm text-[var(--muted)]">
                  No results for <span className="font-mono text-white">{query}</span>.
                </div>
              )}
            </div>
          </motion.div>
        </motion.div>
      ) : null}
    </AnimatePresence>
  );
}
