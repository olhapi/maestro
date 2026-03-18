import { useRef, useState } from "react";
import { Upload } from "lucide-react";

import { Button } from "@/components/ui/button";
import { extractClipboardFiles } from "@/lib/clipboard-files";
import { cn } from "@/lib/utils";

export function FilePicker({
  accept,
  ariaLabel,
  buttonLabel = "Choose files",
  className,
  compact,
  disabled,
  multiple,
  onFilesSelected,
  pasteLabel = "Paste files with Cmd/Ctrl+V",
  summary,
}: {
  accept?: string;
  ariaLabel: string;
  buttonLabel?: string;
  className?: string;
  compact?: boolean;
  disabled?: boolean;
  multiple?: boolean;
  onFilesSelected: (files: File[]) => void;
  pasteLabel?: string;
  summary: string;
}) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [dragActive, setDragActive] = useState(false);

  return (
    <div
      className={cn(
        "flex w-full rounded-[calc(var(--panel-radius)-0.125rem)] border border-dashed border-white/12 bg-black/20 transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent)]",
        compact ? "min-h-0 items-center px-4 py-3" : "min-h-40 items-center justify-center px-6 py-8",
        dragActive && "border-[var(--accent)] bg-[rgba(196,255,87,0.08)]",
        disabled && "opacity-60",
        className,
      )}
      tabIndex={disabled ? -1 : 0}
      onDragEnter={(event) => {
        event.preventDefault();
        if (!disabled) {
          setDragActive(true);
        }
      }}
      onDragLeave={(event) => {
        event.preventDefault();
        if (!disabled) {
          setDragActive(false);
        }
      }}
      onDragOver={(event) => {
        event.preventDefault();
      }}
      onDrop={(event) => {
        event.preventDefault();
        if (disabled) {
          return;
        }
        setDragActive(false);
        const files = Array.from(event.dataTransfer.files ?? []);
        if (files.length > 0) {
          onFilesSelected(files);
        }
      }}
      onPaste={(event) => {
        if (disabled) {
          return;
        }
        const files = extractClipboardFiles(event.clipboardData);
        if (files.length > 0) {
          event.preventDefault();
          onFilesSelected(files);
        }
      }}
    >
      <input
        ref={inputRef}
        aria-label={ariaLabel}
        className="sr-only"
        accept={accept}
        disabled={disabled}
        multiple={multiple}
        type="file"
        onChange={(event) => {
          const files = Array.from(event.currentTarget.files ?? []);
          if (files.length > 0) {
            onFilesSelected(files);
          }
          event.currentTarget.value = "";
        }}
      />
      {compact ? (
        <div className="flex w-full flex-wrap items-center gap-3">
          <div className="flex size-10 items-center justify-center rounded-full border border-white/10 bg-white/5 text-[var(--accent)]">
            <Upload className="size-4" />
          </div>
          <div className="min-w-0 flex-1">
            <p className="text-sm font-medium text-white">{summary}</p>
            <p className="text-xs text-[var(--muted-foreground)]">{pasteLabel}</p>
          </div>
          <Button
            type="button"
            disabled={disabled}
            onClick={() => inputRef.current?.click()}
          >
            <Upload className="size-4" />
            {buttonLabel}
          </Button>
        </div>
      ) : (
        <div className="flex max-w-md flex-col items-center gap-3 text-center">
          <div className="flex size-12 items-center justify-center rounded-full border border-white/10 bg-white/5 text-[var(--accent)]">
            <Upload className="size-5" />
          </div>
          <div className="space-y-1">
            <p className="text-sm font-medium text-white">Drop files here</p>
            <p className="text-sm leading-6 text-[var(--muted-foreground)]">{summary}</p>
            <p className="text-xs text-[var(--muted-foreground)]">{pasteLabel}</p>
          </div>
          <Button
            type="button"
            size="lg"
            className="min-w-44"
            disabled={disabled}
            onClick={() => inputRef.current?.click()}
          >
            <Upload className="size-4" />
            {buttonLabel}
          </Button>
        </div>
      )}
    </div>
  );
}
