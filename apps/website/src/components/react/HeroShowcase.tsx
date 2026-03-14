import React from "react";
import { motion, useReducedMotion } from "motion/react";

import { createRevealMotion } from "../../lib/motion";

export default function HeroShowcase() {
  const reducedMotion = useReducedMotion() ?? false;
  const shot = createRevealMotion(reducedMotion, 0.1);

  return (
    <div className="relative grid gap-4 lg:min-h-[34rem]">
      <div className="pointer-events-none absolute inset-0 -z-10 rounded-[2rem] bg-[radial-gradient(circle_at_top_left,rgba(196,255,87,.2),transparent_38%),radial-gradient(circle_at_bottom_right,rgba(83,217,255,.16),transparent_32%)] blur-3xl" />

      <motion.div
        animate={shot.animate}
        className="panel ml-auto overflow-hidden rounded-4xl w-full"
        initial={shot.initial}
        transition={shot.transition}
      >
        <div className="flex items-center gap-2 border-b border-white/8 px-5 py-3">
          <span className="size-2.5 rounded-full bg-[var(--danger)]" />
          <span className="size-2.5 rounded-full bg-amber-300" />
          <span className="size-2.5 rounded-full bg-[rgb(196,255,87)]" />
          <span className="ml-2 text-xs uppercase tracking-[0.18em] text-[var(--muted)]">
            Live control center
          </span>
        </div>
        <img
          alt="Maestro work board control center view with the shared issue composer open"
          className="w-full object-contain"
          src="/images/screens/work-control-center-speech.png"
        />
      </motion.div>
    </div>
  );
}
