import React from "react";
import { motion, useReducedMotion } from "motion/react";

import { createRevealMotion } from "../../lib/motion";

interface Surface {
  title: string;
  description: string;
}

interface Step {
  title: string;
  command: string;
  detail: string;
}

export default function HeroShowcase({
  quickstartSteps,
  surfaces,
}: {
  quickstartSteps: readonly Step[];
  surfaces: readonly Surface[];
}) {
  const reducedMotion = useReducedMotion() ?? false;
  const cardOne = createRevealMotion(reducedMotion, 0.05);
  const shot = createRevealMotion(reducedMotion, 0.1);
  const cardTwo = createRevealMotion(reducedMotion, 0.18);

  return (
    <div className="relative grid gap-4 lg:min-h-[34rem]">
      <div className="pointer-events-none absolute inset-0 -z-10 rounded-[2rem] bg-[radial-gradient(circle_at_top_left,rgba(196,255,87,.2),transparent_38%),radial-gradient(circle_at_bottom_right,rgba(83,217,255,.16),transparent_32%)] blur-3xl" />

      <motion.div
        animate={cardOne.animate}
        className="panel p-5 lg:absolute lg:left-0 lg:top-8 lg:z-10 lg:w-64"
        initial={cardOne.initial}
        transition={cardOne.transition}
      >
        <p className="kicker !mb-3 !text-[0.72rem]">Quickstart path</p>
        <div className="space-y-4">
          {quickstartSteps.slice(0, 2).map((step, index) => (
            <div key={step.title} className="rounded-[1.2rem] border border-white/8 bg-black/20 p-4">
              <div className="flex items-center gap-3">
                <div className="flex size-8 items-center justify-center rounded-full border border-[rgba(196,255,87,0.25)] bg-[rgba(196,255,87,0.1)] text-sm font-semibold text-[var(--accent-strong)]">
                  {index + 1}
                </div>
                <p className="font-medium text-white">{step.title}</p>
              </div>
              <p className="mt-3 font-mono text-xs leading-6 text-[var(--muted-strong)]">{step.command}</p>
            </div>
          ))}
        </div>
      </motion.div>

      <motion.div
        animate={shot.animate}
        className="panel ml-auto overflow-hidden rounded-[2rem] lg:w-[32rem]"
        initial={shot.initial}
        transition={shot.transition}
      >
        <div className="flex items-center gap-2 border-b border-white/8 px-5 py-3">
          <span className="size-2.5 rounded-full bg-[var(--danger)]" />
          <span className="size-2.5 rounded-full bg-amber-300" />
          <span className="size-2.5 rounded-full bg-[rgb(196,255,87)]" />
          <span className="ml-2 text-xs uppercase tracking-[0.18em] text-[var(--muted)]">Live control center</span>
        </div>
        <img
          alt="Maestro issue detail control center view"
          className="h-full w-full object-cover"
          src="/images/screens/issue-detail.png"
        />
      </motion.div>

      <motion.div
        animate={cardTwo.animate}
        className="panel p-5 lg:absolute lg:bottom-8 lg:right-0 lg:w-72"
        initial={cardTwo.initial}
        transition={cardTwo.transition}
      >
        <p className="kicker !mb-3 !text-[0.72rem]">What stays visible</p>
        <div className="space-y-3">
          {surfaces.map((surface) => (
            <div key={surface.title} className="rounded-[1.15rem] border border-white/8 bg-black/20 p-4">
              <p className="font-display text-lg font-semibold tracking-tight text-white">{surface.title}</p>
              <p className="mt-2 text-sm leading-6 text-[var(--muted)]">{surface.description}</p>
            </div>
          ))}
        </div>
      </motion.div>
    </div>
  );
}
