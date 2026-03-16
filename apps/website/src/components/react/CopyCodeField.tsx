import React, { useEffect, useState } from "react";

async function copyText(value: string) {
  if (
    typeof navigator === "undefined" ||
    typeof navigator.clipboard?.writeText !== "function"
  ) {
    return false;
  }

  try {
    await navigator.clipboard.writeText(value);
    return true;
  } catch {
    return false;
  }
}

export default function CopyCodeField({
  command,
  className = "",
  dense = false,
}: {
  command: string;
  className?: string;
  dense?: boolean;
}) {
  const [copied, setCopied] = useState(false);
  const [canCopy, setCanCopy] = useState<boolean | null>(null);

  useEffect(() => {
    setCanCopy(
      typeof navigator !== "undefined" &&
        typeof navigator.clipboard?.writeText === "function",
    );
  }, []);

  useEffect(() => {
    if (!copied) return undefined;
    const handle = window.setTimeout(() => setCopied(false), 1400);
    return () => window.clearTimeout(handle);
  }, [copied]);

  async function onCopy() {
    if (await copyText(command)) {
      setCopied(true);
    }
  }

  const buttonLabel = copied
    ? "Copied"
    : canCopy === false
      ? "Manual copy"
      : "Copy";
  const wrapperClassName = className ? `relative ${className}` : "relative";
  const buttonSizeClass = dense ? "right-1.5 size-7" : "right-3 size-9";
  const iconSizeClass = dense ? "size-3.5" : "size-4";
  const codeBlockClass = dense
    ? "w-full max-w-full overflow-x-auto rounded-[0.9rem] border border-white/8 bg-black/35 px-3 py-2 pr-12 font-mono text-[0.92rem] leading-6 text-white m-0!"
    : "w-full max-w-full overflow-x-auto rounded-[1.1rem] border border-white/8 bg-black/35 p-4 pr-16 font-mono text-sm text-white m-0!";

  return (
    <div className={wrapperClassName}>
      <button
        aria-label={buttonLabel}
        className={`absolute top-1/2 z-10 flex -translate-y-1/2 items-center justify-center rounded-full border text-white transition ${buttonSizeClass} ${
          copied
            ? "border-[rgba(196,255,87,0.32)] bg-[rgba(196,255,87,0.14)]"
            : "border-white/10 bg-[rgb(24,25,29)] hover:border-white/16 hover:bg-[rgb(31,32,37)]"
        } cursor-pointer disabled:cursor-not-allowed disabled:opacity-55`}
        disabled={canCopy === false}
        onClick={onCopy}
        title={buttonLabel}
        type="button"
      >
        <span className="sr-only">{buttonLabel}</span>
        {copied ? (
          <svg
            aria-hidden="true"
            className={iconSizeClass}
            fill="none"
            viewBox="0 0 16 16"
          >
            <path
              d="M3.75 8.25 6.5 11l5.75-6.25"
              stroke="currentColor"
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth="1.5"
            />
          </svg>
        ) : (
          <svg
            aria-hidden="true"
            className={iconSizeClass}
            fill="none"
            viewBox="0 0 16 16"
          >
            <rect
              height="8.5"
              rx="1.5"
              stroke="currentColor"
              strokeWidth="1.25"
              width="7.5"
              x="5.25"
              y="3.25"
            />
            <path
              d="M4.75 10.75h-1A1.5 1.5 0 0 1 2.25 9.25v-5A1.5 1.5 0 0 1 3.75 2.75h5A1.5 1.5 0 0 1 10.25 4v.75"
              stroke="currentColor"
              strokeLinecap="round"
              strokeWidth="1.25"
            />
          </svg>
        )}
      </button>
      <pre className={codeBlockClass}>
        <code>{command}</code>
      </pre>
    </div>
  );
}
