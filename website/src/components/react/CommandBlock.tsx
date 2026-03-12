import React, { useEffect, useState } from "react";

async function copyText(value: string) {
  if (typeof navigator === "undefined" || typeof navigator.clipboard?.writeText !== "function") {
    return false;
  }

  try {
    await navigator.clipboard.writeText(value);
    return true;
  } catch {
    return false;
  }
}

export default function CommandBlock({
  command,
  detail,
  title,
}: {
  command: string;
  detail: string;
  title: string;
}) {
  const [copied, setCopied] = useState(false);
  const [canCopy, setCanCopy] = useState<boolean | null>(null);

  useEffect(() => {
    setCanCopy(typeof navigator !== "undefined" && typeof navigator.clipboard?.writeText === "function");
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

  return (
    <div className="panel h-full p-5">
      <div className="flex items-start justify-between gap-4">
        <div>
          <p className="kicker !mb-3 !text-[0.72rem]">{title}</p>
          <p className="text-sm leading-6 text-[var(--muted)]">{detail}</p>
        </div>
        <button
          className="rounded-full border border-white/10 bg-[rgba(255,255,255,0.06)] px-3 py-1.5 text-xs font-medium text-white transition hover:border-white/16 hover:bg-[rgba(255,255,255,0.09)] disabled:cursor-not-allowed disabled:opacity-55"
          disabled={canCopy === false}
          onClick={onCopy}
          type="button"
        >
          {copied ? "Copied" : canCopy === false ? "Manual copy" : "Copy"}
        </button>
      </div>
      <pre className="mt-5 overflow-x-auto rounded-[1.1rem] border border-white/8 bg-black/35 p-4 font-mono text-sm text-white">
        <code>{command}</code>
      </pre>
    </div>
  );
}
