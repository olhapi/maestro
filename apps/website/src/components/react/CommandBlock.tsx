import React from "react";
import CopyCodeField from "./CopyCodeField";

export default function CommandBlock({
  command,
  detail,
  title,
}: {
  command: string;
  detail: string;
  title: string;
}) {
  return (
    <div className="panel min-w-0 h-full p-5">
      <div className="min-w-0">
        <p className="kicker !mb-3 !text-[0.72rem]">{title}</p>
        <p className="text-sm leading-6 text-[var(--muted)]">{detail}</p>
      </div>
      <CopyCodeField className="mt-5" command={command} />
    </div>
  );
}
