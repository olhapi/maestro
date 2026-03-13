import React from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useEffect, useMemo, useRef, useState } from "react";

interface Chapter {
  id: string;
  eyebrow?: string;
  title: string;
  description: string;
  bullets: readonly string[];
  image: string;
}

export default function TourScroller({
  chapters,
  mode,
}: {
  chapters: readonly Chapter[];
  mode: "home" | "tour";
}) {
  const reducedMotion = useReducedMotion() ?? false;
  const [activeIndex, setActiveIndex] = useState(0);
  const refs = useRef<Array<HTMLDivElement | null>>([]);

  useEffect(() => {
    const observers = refs.current
      .filter((value): value is HTMLDivElement => Boolean(value))
      .map((element, index) => {
        const observer = new IntersectionObserver(
          (entries) => {
            entries.forEach((entry) => {
              if (entry.isIntersecting) {
                setActiveIndex(index);
              }
            });
          },
          { rootMargin: "-30% 0px -45% 0px", threshold: 0.25 },
        );
        observer.observe(element);
        return observer;
      });

    return () => {
      observers.forEach((observer) => observer.disconnect());
    };
  }, [chapters.length]);

  const activeChapter = useMemo(() => chapters[activeIndex] ?? chapters[0], [activeIndex, chapters]);
  const desktopSectionHeight = mode === "tour" ? "min-h-[34rem]" : "min-h-[28rem]";

  return (
    <>
      <div className="grid gap-4 lg:hidden">
        {chapters.map((chapter) => (
          <div key={chapter.id} className="panel overflow-hidden p-5">
            <img alt={chapter.title} className="rounded-[1.2rem] border border-white/8" src={chapter.image} />
            {chapter.eyebrow ? <p className="eyebrow mt-5">{chapter.eyebrow}</p> : null}
            <h3 className="font-display text-2xl font-semibold tracking-tight text-white">{chapter.title}</h3>
            <p className="mt-3 text-sm leading-7 text-[var(--muted)]">{chapter.description}</p>
            <ul className="mt-4 space-y-2 text-sm leading-6 text-[var(--muted-strong)]">
              {chapter.bullets.map((bullet) => (
                <li key={bullet} className="flex gap-3">
                  <span className="mt-2 size-1.5 shrink-0 rounded-full bg-[rgb(196,255,87)]" />
                  <span>{bullet}</span>
                </li>
              ))}
            </ul>
          </div>
        ))}
      </div>

      <div className="hidden gap-8 lg:grid lg:grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)]">
        <div className="space-y-6">
          {chapters.map((chapter, index) => (
            <div
              key={chapter.id}
              className={`${desktopSectionHeight} flex items-center`}
              ref={(node) => {
                refs.current[index] = node;
              }}
            >
              <div
                className={`panel w-full p-6 transition ${
                  activeIndex === index ? "border-[rgba(196,255,87,0.24)]" : "opacity-75"
                }`}
              >
                {chapter.eyebrow ? <p className="eyebrow">{chapter.eyebrow}</p> : null}
                <h3 className="font-display text-[2rem] font-semibold leading-[1.02] tracking-tight text-white">
                  {chapter.title}
                </h3>
                <p className="mt-4 text-base leading-7 text-[var(--muted)]">{chapter.description}</p>
                <ul className="mt-5 space-y-2.5 text-sm leading-6 text-[var(--muted-strong)]">
                  {chapter.bullets.map((bullet) => (
                    <li key={bullet} className="flex gap-3">
                      <span className="mt-2 size-1.5 shrink-0 rounded-full bg-[rgb(196,255,87)]" />
                      <span>{bullet}</span>
                    </li>
                  ))}
                </ul>
              </div>
            </div>
          ))}
        </div>

        <div className="sticky top-28 h-fit">
          <div className="panel overflow-hidden p-5">
            <AnimatePresence mode="wait">
              <motion.div
                key={activeChapter.id}
                animate={{ opacity: 1, y: 0 }}
                initial={reducedMotion ? { opacity: 1, y: 0 } : { opacity: 0, y: 18 }}
                transition={reducedMotion ? { duration: 0 } : { duration: 0.38, ease: "easeOut" }}
                exit={reducedMotion ? { opacity: 1 } : { opacity: 0, y: -12 }}
              >
                <img
                  alt={activeChapter.title}
                  className="rounded-[1.35rem] border border-white/8"
                  src={activeChapter.image}
                />
                <div className="mt-5 flex items-center justify-between gap-4">
                  <div>
                    {activeChapter.eyebrow ? <p className="eyebrow">{activeChapter.eyebrow}</p> : null}
                    <h4 className="font-display text-2xl font-semibold tracking-tight text-white">
                      {activeChapter.title}
                    </h4>
                  </div>
                  <span className="rounded-full border border-white/10 px-3 py-1 text-xs uppercase tracking-[0.16em] text-[var(--muted)]">
                    {activeIndex + 1} / {chapters.length}
                  </span>
                </div>
              </motion.div>
            </AnimatePresence>
          </div>
        </div>
      </div>
    </>
  );
}
