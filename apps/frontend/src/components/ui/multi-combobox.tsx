import { Combobox } from '@base-ui/react/combobox'
import { Check, ChevronsUpDown, Search, X } from 'lucide-react'
import { useId, useMemo, useRef, useState } from 'react'

import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

export interface MultiComboboxOption {
  value: string
  label: string
  keywords?: string[]
}

function normalize(value: string) {
  return value.trim().toLowerCase()
}

function matchesOption(option: MultiComboboxOption, query: string) {
  const normalized = normalize(query)
  if (!normalized) return true
  return [option.value, option.label, ...(option.keywords ?? [])].some((candidate) => normalize(candidate).includes(normalized))
}

export function MultiCombobox({
  ariaLabel,
  labelledBy,
  disabled,
  emptyText,
  loading = false,
  onChange,
  options,
  placeholder,
  value,
  allowCreate = false,
  createLabel,
}: {
  ariaLabel?: string
  labelledBy?: string
  disabled?: boolean
  emptyText: string
  loading?: boolean
  onChange: (next: string[]) => void
  options: MultiComboboxOption[]
  placeholder: string
  value: string[]
  allowCreate?: boolean
  createLabel?: (value: string) => string
}) {
  const fallbackId = useId()
  const listboxId = `${fallbackId}-listbox`
  const [open, setOpen] = useState(false)
  const [inputValue, setInputValue] = useState('')
  const inputRef = useRef<HTMLInputElement | null>(null)
  const portalContainerRef = useRef<HTMLDivElement | null>(null)
  const ignoreNextEmptyOpenRef = useRef(false)

  const selectedSet = useMemo(() => new Set(value), [value])
  const optionMap = useMemo(() => new Map(options.map((option) => [option.value, option])), [options])
  const selectedOptions = value.map((selected) => optionMap.get(selected) ?? { value: selected, label: selected })

  const filteredOptions = options.filter((option) => !selectedSet.has(option.value) && matchesOption(option, inputValue))
  const trimmedInput = inputValue.trim()
  const canCreate = allowCreate && trimmedInput.length > 0 && !selectedSet.has(trimmedInput) && !options.some((option) => normalize(option.value) === normalize(trimmedInput) || normalize(option.label) === normalize(trimmedInput))

  return (
    <Combobox.Root
      multiple
      modal={false}
      open={disabled ? false : open}
      value={value}
      inputValue={inputValue}
      disabled={disabled}
      onOpenChange={(nextOpen) => {
        if (!disabled) {
          setOpen(nextOpen)
        }
      }}
      onInputValueChange={(nextValue) => {
        setInputValue(nextValue)
        if (ignoreNextEmptyOpenRef.current && nextValue === '') {
          ignoreNextEmptyOpenRef.current = false
          setOpen(false)
          return
        }

        ignoreNextEmptyOpenRef.current = false

        if (!disabled && nextValue.trim().length > 0) {
          setOpen(true)
        }
      }}
      onValueChange={(nextValue) => {
        onChange(nextValue ?? [])
        ignoreNextEmptyOpenRef.current = true
        setInputValue('')
        setOpen(false)
        inputRef.current?.blur()
      }}
    >
      <Combobox.InputGroup
        className={cn(
          'relative rounded-xl border border-white/10 bg-black/20 transition focus-within:border-[var(--accent)]',
          disabled && 'cursor-not-allowed opacity-50',
        )}
      >
        <div className="flex min-h-11 flex-wrap items-center gap-2 px-3 py-2 pr-12">
          {selectedOptions.map((option) => (
            <span
              key={option.value}
              className="inline-flex items-center gap-1.5 rounded-full border border-white/10 bg-white/6 px-2.5 py-1 text-xs text-white"
            >
              <span className="max-w-[14rem] truncate">{option.label}</span>
              <button
                type="button"
                className="rounded-full p-0.5 text-[var(--muted-foreground)] transition hover:bg-white/8 hover:text-white disabled:cursor-not-allowed"
                onClick={() => onChange(value.filter((item) => item !== option.value))}
                disabled={disabled}
                aria-label={`Remove ${option.label}`}
              >
                <X className="size-3.5" />
              </button>
            </span>
          ))}
          <div className="flex min-w-[12rem] flex-1 items-center gap-2">
            <Search className="size-4 shrink-0 text-[var(--muted-foreground)]" />
            <Combobox.Input
              ref={inputRef}
              aria-label={ariaLabel}
              aria-labelledby={labelledBy}
              aria-controls={listboxId}
              placeholder={selectedOptions.length > 0 ? 'Add more…' : placeholder}
              className="h-7 flex-1 bg-transparent text-sm text-white outline-none placeholder:text-[var(--muted-foreground)]"
              onFocus={() => {
                if (!disabled) {
                  setOpen(true)
                }
              }}
              onClick={() => {
                if (!disabled) {
                  setOpen(true)
                }
              }}
            />
          </div>
        </div>
        <Combobox.Trigger
          aria-hidden="true"
          className="absolute right-3 top-1/2 -translate-y-1/2 rounded-lg p-1.5 text-[var(--muted-foreground)] transition hover:bg-white/8 hover:text-white disabled:cursor-not-allowed"
        >
          <ChevronsUpDown className="size-4" />
        </Combobox.Trigger>
      </Combobox.InputGroup>
      <div ref={portalContainerRef} />

      <Combobox.Portal container={portalContainerRef}>
        <Combobox.Positioner sideOffset={8} className="z-[60] w-[var(--anchor-width)] pointer-events-auto">
          <Combobox.Popup className="pointer-events-auto overflow-hidden rounded-2xl border border-white/10 bg-[rgba(11,14,19,.98)] p-1.5 shadow-[0_20px_80px_rgba(0,0,0,.45)]">
            <Combobox.List id={listboxId} className="pointer-events-auto max-h-64 overflow-y-auto">
              {loading ? (
                <div className="px-3 py-8 text-center text-sm text-[var(--muted-foreground)]">Loading…</div>
              ) : null}
              {!loading && filteredOptions.length === 0 && !canCreate ? (
                <div className="px-3 py-8 text-center text-sm text-[var(--muted-foreground)]">{emptyText}</div>
              ) : null}
              {!loading &&
                filteredOptions.map((option) => (
                  <Combobox.Item
                    key={option.value}
                    value={option.value}
                    className="flex cursor-pointer items-center gap-3 rounded-xl px-3 py-2.5 text-sm text-white outline-none transition data-[highlighted]:bg-white/8"
                  >
                    <span className="flex size-4 items-center justify-center">
                      <Combobox.ItemIndicator>
                        <Check className="size-4 text-[var(--accent)]" />
                      </Combobox.ItemIndicator>
                    </span>
                    <span className="truncate">{option.label}</span>
                  </Combobox.Item>
                ))}
              {!loading && canCreate ? (
                <Button
                  type="button"
                  variant="ghost"
                  className="h-auto w-full justify-start rounded-xl border-transparent px-3 py-2.5 text-sm text-white hover:bg-white/8"
                  onClick={() => {
                    onChange([...value, trimmedInput])
                    ignoreNextEmptyOpenRef.current = true
                    setInputValue('')
                    setOpen(false)
                    inputRef.current?.blur()
                  }}
                >
                  <span className="truncate">{createLabel ? createLabel(trimmedInput) : `Add "${trimmedInput}"`}</span>
                </Button>
              ) : null}
            </Combobox.List>
          </Combobox.Popup>
        </Combobox.Positioner>
      </Combobox.Portal>
    </Combobox.Root>
  )
}
