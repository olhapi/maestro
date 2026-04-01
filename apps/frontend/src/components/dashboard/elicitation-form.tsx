import { useLayoutEffect, useMemo, useRef, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import {
  buildElicitationContent,
  createInitialElicitationDraftValues,
  normalizeElicitationRequestedSchema,
  type ElicitationDraftValue,
  type ElicitationPrimitiveNode,
  type ElicitationSchemaNode,
} from '@/lib/elicitation'
import { cn } from '@/lib/utils'

const COMPACT_CHOICE_LIMIT = 4

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' && !Array.isArray(value)
}

function isUnionDraft(value: unknown): value is {
  kind: 'union'
  selectedIndex: number
  branches: ElicitationDraftValue[]
} {
  return (
    isPlainObject(value) &&
    value.kind === 'union' &&
    typeof value.selectedIndex === 'number' &&
    Array.isArray(value.branches)
  )
}

function nodeId(path: string) {
  return `elicitation-${path.replace(/[^a-zA-Z0-9_-]/g, '-')}`
}

function shouldRenderCompactChoiceButtons(optionCount: number) {
  return optionCount > 0 && optionCount <= COMPACT_CHOICE_LIMIT
}

function choiceButtonClass(selected: boolean) {
  return cn(
    'rounded-xl border px-3 py-2 text-left text-sm transition',
    selected
      ? 'border-[var(--accent)]/50 bg-[var(--accent)]/10 text-white'
      : 'border-white/10 bg-black/20 text-[var(--muted-foreground)] hover:border-white/20 hover:text-white',
  )
}

function nodeConstraintSummary(node: ElicitationSchemaNode) {
  const parts: string[] = []

  if (node.kind === 'primitive') {
    if (node.fieldType === 'string') {
      if (typeof node.minLength === 'number') {
        parts.push(`Minimum ${node.minLength} characters`)
      }
      if (typeof node.maxLength === 'number') {
        parts.push(`Maximum ${node.maxLength} characters`)
      }
    }

    if (node.fieldType === 'number' || node.fieldType === 'integer') {
      if (typeof node.minimum === 'number') {
        parts.push(`Minimum ${node.minimum}`)
      }
      if (typeof node.maximum === 'number') {
        parts.push(`Maximum ${node.maximum}`)
      }
    }

    if (node.fieldType === 'multi_select') {
      if (typeof node.minItems === 'number') {
        parts.push(`Select at least ${node.minItems}`)
      }
      if (typeof node.maxItems === 'number') {
        parts.push(`Up to ${node.maxItems} selections`)
      }
    }
  }

  if (node.kind === 'array') {
    if (typeof node.minItems === 'number') {
      parts.push(`At least ${node.minItems} item${node.minItems === 1 ? '' : 's'}`)
    }
    if (typeof node.maxItems === 'number') {
      parts.push(`Up to ${node.maxItems} item${node.maxItems === 1 ? '' : 's'}`)
    }
  }

  if (node.kind === 'object') {
    if (typeof node.minProperties === 'number') {
      parts.push(`At least ${node.minProperties} propert${node.minProperties === 1 ? 'y' : 'ies'}`)
    }
    if (typeof node.maxProperties === 'number') {
      parts.push(`Up to ${node.maxProperties} propert${node.maxProperties === 1 ? 'y' : 'ies'}`)
    }
  }

  return parts.length > 0 ? parts.join('. ') : ''
}

function nodeHelpText(node: ElicitationSchemaNode) {
  if (node.description) {
    return node.description
  }

  if (node.kind === 'primitive') {
    switch (node.fieldType) {
      case 'single_select':
        return 'Choose one option.'
      case 'multi_select':
        return 'Choose one or more options.'
      case 'boolean':
        return 'Toggle the requested flag.'
      case 'number':
      case 'integer':
        return 'Enter a numeric value.'
      default:
        return ''
    }
  }

  if (node.kind === 'array') {
    return 'Add one or more items.'
  }

  if (node.kind === 'union') {
    return 'Choose one branch.'
  }

  return ''
}

function primitiveInputType(node: ElicitationPrimitiveNode) {
  if (node.fieldType === 'number' || node.fieldType === 'integer') {
    return 'number'
  }
  if (node.fieldType === 'string') {
    if (node.format === 'email') {
      return 'email'
    }
    if (node.format === 'uri') {
      return 'url'
    }
    if (node.format === 'date') {
      return 'date'
    }
    if (node.format === 'date-time') {
      return 'datetime-local'
    }
  }
  return 'text'
}

type ElicitationFormDraftState = {
  draftValues: ElicitationDraftValue
  manualContent: string
}

function createInitialDraftState(analysis: ReturnType<typeof normalizeElicitationRequestedSchema>): ElicitationFormDraftState {
  return {
    draftValues: analysis.state === 'ready' ? createInitialElicitationDraftValues(analysis.node) : {},
    manualContent: '',
  }
}

function updateObjectValue(current: unknown, key: string, nextValue: ElicitationDraftValue) {
  const next: Record<string, ElicitationDraftValue> = isPlainObject(current)
    ? { ...(current as Record<string, ElicitationDraftValue>) }
    : {}
  next[key] = nextValue
  return next
}

function updateArrayValue(current: unknown, index: number, nextValue: ElicitationDraftValue) {
  const next: ElicitationDraftValue[] = Array.isArray(current) ? [...(current as ElicitationDraftValue[])] : []
  next[index] = nextValue
  return next
}

function removeArrayValue(current: unknown, index: number) {
  const next: ElicitationDraftValue[] = Array.isArray(current) ? [...(current as ElicitationDraftValue[])] : []
  next.splice(index, 1)
  return next
}

function appendArrayValue(current: unknown, nextValue: ElicitationDraftValue) {
  const next: ElicitationDraftValue[] = Array.isArray(current) ? [...(current as ElicitationDraftValue[])] : []
  next.push(nextValue)
  return next
}

function PrimitiveControl({
  disabled,
  node,
  value,
  path,
  onChange,
}: {
  disabled: boolean
  node: ElicitationPrimitiveNode
  value: unknown
  path: string
  onChange: (value: ElicitationDraftValue) => void
}) {
  const fieldId = nodeId(path)
  const labelId = `${fieldId}-label`
  const descriptionId = `${fieldId}-description`
  const constraintText = nodeConstraintSummary(node)
  const helpText = nodeHelpText(node)

  const description = helpText ? (
    <p className="text-sm leading-6 text-[var(--muted-foreground)]" id={descriptionId}>
      {helpText}
    </p>
  ) : null

  if (node.fieldType === 'boolean') {
    const checked = typeof value === 'boolean' ? value : typeof node.defaultValue === 'boolean' ? node.defaultValue : false

    return (
      <div className="grid gap-3 rounded-[calc(var(--panel-radius)-0.25rem)] border border-white/10 bg-black/20 p-4">
        <div className="flex items-start justify-between gap-4">
          <div className="grid gap-1">
            <Label
              className="text-[11px] uppercase tracking-[0.16em] text-[var(--muted-foreground)]"
              htmlFor={fieldId}
              id={labelId}
            >
              {node.label}
            </Label>
            {description}
            {constraintText ? <p className="text-xs leading-5 text-[var(--muted-foreground)]">{constraintText}</p> : null}
          </div>
          <Switch
            aria-labelledby={labelId}
            checked={checked}
            disabled={disabled}
            id={fieldId}
            onCheckedChange={(nextChecked) => {
              onChange(nextChecked)
            }}
          />
        </div>
      </div>
    )
  }

  if (node.fieldType === 'single_select') {
    const selectedValue = typeof value === 'string' ? value : ''
    const renderChoiceButtons = shouldRenderCompactChoiceButtons(node.options?.length ?? 0)

    return (
      <div className="grid gap-3 rounded-[calc(var(--panel-radius)-0.25rem)] border border-white/10 bg-black/20 p-4">
        <div className="grid gap-1">
          <Label
            className="text-[11px] uppercase tracking-[0.16em] text-[var(--muted-foreground)]"
            htmlFor={fieldId}
            id={labelId}
          >
            {node.label}
          </Label>
          {description}
          {constraintText ? <p className="text-xs leading-5 text-[var(--muted-foreground)]">{constraintText}</p> : null}
        </div>
        {renderChoiceButtons ? (
          <div
            aria-describedby={node.description || constraintText ? descriptionId : undefined}
            aria-labelledby={labelId}
            className="grid gap-2 sm:grid-cols-2"
            id={fieldId}
            role="group"
          >
            {node.options?.map((option) => {
              const selected = selectedValue === option.value
              return (
                <button
                  key={option.value}
                  aria-pressed={selected}
                  className={choiceButtonClass(selected)}
                  disabled={disabled}
                  type="button"
                  onClick={() => {
                    onChange(option.value)
                  }}
                >
                  <span className="font-medium">{option.label}</span>
                </button>
              )
            })}
          </div>
        ) : (
          <Select
            disabled={disabled}
            value={selectedValue}
            onValueChange={(nextValue) => {
              onChange(nextValue)
            }}
          >
            <SelectTrigger aria-describedby={node.description || constraintText ? descriptionId : undefined} aria-labelledby={labelId} id={fieldId}>
              <SelectValue placeholder={node.required ? 'Choose an option' : 'Optional'} />
            </SelectTrigger>
            <SelectContent>
              {node.options?.map((option) => (
                <SelectItem key={option.value} value={option.value}>
                  {option.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
      </div>
    )
  }

  if (node.fieldType === 'multi_select') {
    const selectedValues = Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : []

    return (
      <div className="grid gap-3 rounded-[calc(var(--panel-radius)-0.25rem)] border border-white/10 bg-black/20 p-4">
        <div className="grid gap-1">
          <Label className="text-[11px] uppercase tracking-[0.16em] text-[var(--muted-foreground)]" id={labelId}>
            {node.label}
          </Label>
          {description}
          {constraintText ? <p className="text-xs leading-5 text-[var(--muted-foreground)]">{constraintText}</p> : null}
        </div>
        <ToggleGroup
          aria-describedby={node.description || constraintText ? descriptionId : undefined}
          aria-labelledby={labelId}
          className="flex flex-wrap gap-2"
          type="multiple"
          value={selectedValues}
          onValueChange={(nextValues) => {
            onChange(nextValues)
          }}
        >
          {node.options?.map((option) => (
            <ToggleGroupItem
              key={option.value}
              aria-label={option.label}
              className={cn(
                'rounded-full border border-white/10 bg-white/[0.03] px-3.5 py-2 text-sm text-white/80 transition',
                'data-[state=on]:border-[var(--accent)]/35 data-[state=on]:bg-[linear-gradient(135deg,rgba(196,255,87,.24),rgba(255,255,255,.08))] data-[state=on]:text-white',
              )}
              value={option.value}
            >
              {option.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      </div>
    )
  }

  const inputType = primitiveInputType(node)
  const inputValue = typeof value === 'string' ? value : ''

  return (
    <div className="grid gap-3 rounded-[calc(var(--panel-radius)-0.25rem)] border border-white/10 bg-black/20 p-4">
      <div className="grid gap-1">
        <Label
          className="text-[11px] uppercase tracking-[0.16em] text-[var(--muted-foreground)]"
          htmlFor={fieldId}
          id={labelId}
        >
          {node.label}
        </Label>
        {description}
        {constraintText ? <p className="text-xs leading-5 text-[var(--muted-foreground)]">{constraintText}</p> : null}
      </div>
      <Input
        aria-describedby={node.description || constraintText ? descriptionId : undefined}
        aria-labelledby={labelId}
        autoComplete="off"
        disabled={disabled}
        id={fieldId}
        inputMode={node.fieldType === 'number' || node.fieldType === 'integer' ? 'decimal' : undefined}
        max={node.fieldType === 'number' || node.fieldType === 'integer' ? node.maximum : undefined}
        maxLength={node.fieldType === 'string' ? node.maxLength : undefined}
        min={node.fieldType === 'number' || node.fieldType === 'integer' ? node.minimum : undefined}
        minLength={node.fieldType === 'string' ? node.minLength : undefined}
        placeholder={node.required ? node.label : 'Optional'}
        required={node.required}
        step={node.fieldType === 'integer' ? 1 : node.fieldType === 'number' ? 'any' : undefined}
        type={inputType}
        value={inputValue}
        onChange={(event) => {
          onChange(event.target.value)
        }}
      />
    </div>
  )
}

function SchemaNodeEditor({
  disabled,
  isRoot,
  node,
  path,
  value,
  onChange,
}: {
  disabled: boolean
  isRoot: boolean
  node: ElicitationSchemaNode
  path: string
  value: ElicitationDraftValue
  onChange: (value: ElicitationDraftValue) => void
}) {
  if (node.kind === 'primitive') {
    return <PrimitiveControl disabled={disabled} node={node} value={value} path={path} onChange={onChange} />
  }

  if (node.kind === 'union') {
    const currentDraft = isUnionDraft(value)
      ? value
      : {
          kind: 'union' as const,
          selectedIndex: 0,
          branches: node.options.map((option) => createInitialElicitationDraftValues(option.node)),
        }
    const selectedIndex = Math.min(Math.max(currentDraft.selectedIndex, 0), node.options.length - 1)
    const selectedOption = node.options[selectedIndex]
    const unionId = nodeId(path)
    const labelId = `${unionId}-label`
    const descriptionId = `${unionId}-description`
    const helpText = nodeHelpText(node)
    const constraintText = nodeConstraintSummary(node)
    const renderChoiceButtons = shouldRenderCompactChoiceButtons(node.options.length)

    return (
      <div className="grid gap-4 rounded-[calc(var(--panel-radius)-0.2rem)] border border-white/10 bg-white/[0.03] p-4">
        <div className="grid gap-1">
          {isRoot && node.label ? (
            <p className="text-sm font-medium text-white">{node.label}</p>
          ) : !isRoot ? (
            <p className="text-sm font-medium text-white">{node.label}</p>
          ) : null}
          {helpText ? (
            <p className="text-sm leading-6 text-[var(--muted-foreground)]" id={descriptionId}>
              {helpText}
            </p>
          ) : null}
          {constraintText ? <p className="text-xs leading-5 text-[var(--muted-foreground)]">{constraintText}</p> : null}
        </div>
        <div className="grid gap-2">
          <Label className="text-[11px] uppercase tracking-[0.16em] text-[var(--muted-foreground)]" id={labelId}>
            Branch
          </Label>
          {renderChoiceButtons ? (
            <div
              aria-describedby={node.description || constraintText ? descriptionId : undefined}
              aria-labelledby={labelId}
              className="grid gap-2 sm:grid-cols-2"
              id={`${unionId}-selector`}
              role="group"
            >
              {node.options.map((option, index) => {
                const selected = index === selectedIndex
                return (
                  <button
                    key={`${path}-${index}`}
                    aria-pressed={selected}
                    className={choiceButtonClass(selected)}
                    disabled={disabled}
                    type="button"
                    onClick={() => {
                      onChange({
                        ...currentDraft,
                        selectedIndex: index,
                      })
                    }}
                  >
                    <span className="font-medium">{option.label}</span>
                    {option.description ? (
                      <span className="mt-1 block text-[var(--muted-foreground)]">{option.description}</span>
                    ) : null}
                  </button>
                )
              })}
            </div>
          ) : (
            <Select
              disabled={disabled}
              value={String(selectedIndex)}
              onValueChange={(nextValue) => {
                const nextIndex = Number(nextValue)
                if (Number.isNaN(nextIndex)) {
                  return
                }
                onChange({
                  ...currentDraft,
                  selectedIndex: nextIndex,
                })
              }}
            >
              <SelectTrigger aria-describedby={node.description || constraintText ? descriptionId : undefined} aria-labelledby={labelId} id={`${unionId}-selector`}>
                <SelectValue placeholder="Choose a branch" />
              </SelectTrigger>
              <SelectContent>
                {node.options.map((option, index) => (
                  <SelectItem key={`${path}-${index}`} value={String(index)}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        </div>
        {selectedOption ? (
          <SchemaNodeEditor
            disabled={disabled}
            isRoot={false}
            node={selectedOption.node}
            path={`${path}::branch-${selectedIndex}`}
            value={currentDraft.branches[selectedIndex] ?? createInitialElicitationDraftValues(selectedOption.node)}
            onChange={(nextValue) => {
              const nextBranches = [...currentDraft.branches]
              nextBranches[selectedIndex] = nextValue
              onChange({
                ...currentDraft,
                branches: nextBranches,
              })
            }}
          />
        ) : null}
      </div>
    )
  }

  if (node.kind === 'array') {
    const items = Array.isArray(value) ? value : []
    const helpText = nodeHelpText(node)
    const constraintText = nodeConstraintSummary(node)
    const sectionId = nodeId(path)
    const addItemDisabled = disabled || (typeof node.maxItems === 'number' && items.length >= node.maxItems)

    return (
      <div className={cn('grid gap-4 rounded-[calc(var(--panel-radius)-0.2rem)] border border-white/10 bg-white/[0.03] p-4')}>
        <div className="grid gap-1">
          <p className="text-sm font-medium text-white">{node.label}</p>
          {helpText ? <p className="text-sm leading-6 text-[var(--muted-foreground)]">{helpText}</p> : null}
          {constraintText ? <p className="text-xs leading-5 text-[var(--muted-foreground)]">{constraintText}</p> : null}
        </div>
        {items.length === 0 ? (
          <p className="text-sm leading-6 text-[var(--muted-foreground)]">No items added yet.</p>
        ) : (
          <div className="grid gap-3">
            {items.map((item, index) => (
              <div key={`${sectionId}-${index}`} className="grid gap-3 rounded-[calc(var(--panel-radius)-0.35rem)] border border-white/8 bg-black/20 p-4">
                <div className="flex items-center justify-end">
                  <Button
                    className="h-8 rounded-full px-3 text-xs"
                    disabled={disabled || (typeof node.minItems === 'number' && items.length <= node.minItems)}
                    type="button"
                    variant="secondary"
                    onClick={() => {
                      onChange(removeArrayValue(items, index))
                    }}
                  >
                    Remove item {index + 1}
                  </Button>
                </div>
                <SchemaNodeEditor
                  disabled={disabled}
                  isRoot={false}
                  node={node.item}
                  path={`${path}[${index}]`}
                  value={item}
                  onChange={(nextValue) => {
                    onChange(updateArrayValue(items, index, nextValue))
                  }}
                />
              </div>
            ))}
          </div>
        )}
        <div className="flex flex-wrap items-center gap-3">
          <Button
            className="h-10 rounded-2xl px-4"
            disabled={addItemDisabled}
            type="button"
            variant="secondary"
            onClick={() => {
              onChange(appendArrayValue(items, createInitialElicitationDraftValues(node.item)))
            }}
          >
            Add item
          </Button>
        </div>
      </div>
    )
  }

  const helpText = nodeHelpText(node)
  const constraintText = nodeConstraintSummary(node)
  const content: Record<string, ElicitationDraftValue> = isPlainObject(value)
    ? (value as Record<string, ElicitationDraftValue>)
    : {}

  return (
    <div className={cn(isRoot ? 'grid gap-4' : 'grid gap-4 rounded-[calc(var(--panel-radius)-0.2rem)] border border-white/10 bg-white/[0.03] p-4')}>
      {!isRoot ? (
        <div className="grid gap-1">
          <p className="text-sm font-medium text-white">{node.label}</p>
          {helpText ? <p className="text-sm leading-6 text-[var(--muted-foreground)]">{helpText}</p> : null}
          {constraintText ? <p className="text-xs leading-5 text-[var(--muted-foreground)]">{constraintText}</p> : null}
        </div>
      ) : helpText || constraintText ? (
        <div className="grid gap-1">
          {helpText ? <p className="text-sm leading-6 text-[var(--muted-foreground)]">{helpText}</p> : null}
          {constraintText ? <p className="text-xs leading-5 text-[var(--muted-foreground)]">{constraintText}</p> : null}
        </div>
      ) : null}

      {node.properties.length === 0 ? (
        <p className="text-sm leading-6 text-[var(--muted-foreground)]">No fields in this section.</p>
      ) : (
        <div className="grid gap-3">
          {node.properties.map((property) => (
            <SchemaNodeEditor
              key={`${path}.${property.name}`}
              disabled={disabled}
              isRoot={false}
              node={property.node}
              path={`${path}.${property.name}`}
              value={content[property.name] as ElicitationDraftValue}
              onChange={(nextValue) => {
                onChange(updateObjectValue(content, property.name, nextValue))
              }}
            />
          ))}
        </div>
      )}
    </div>
  )
}

export function ElicitationForm({
  draftKey,
  requestedSchema,
  disabled,
  isSubmitting,
  onAccept,
  onCancel,
  onDecline,
}: {
  draftKey: string
  requestedSchema: unknown
  disabled: boolean
  isSubmitting: boolean
  onAccept: (content: unknown) => void
  onCancel: () => void
  onDecline: () => void
}) {
  const analysis = useMemo(() => normalizeElicitationRequestedSchema(requestedSchema), [requestedSchema])
  const draftCacheRef = useRef<Record<string, ElicitationFormDraftState>>({})
  const [draftState, setDraftState] = useState<ElicitationFormDraftState>(() => createInitialDraftState(analysis))

  useLayoutEffect(() => {
    const cachedDraft = draftCacheRef.current[draftKey]
    if (cachedDraft) {
      setDraftState(cachedDraft)
      return
    }

    const initialDraftState = createInitialDraftState(analysis)
    draftCacheRef.current[draftKey] = initialDraftState
    setDraftState(initialDraftState)
  }, [analysis, draftKey])

  const updateDraftState = (updater: (current: ElicitationFormDraftState) => ElicitationFormDraftState) => {
    setDraftState((current) => {
      const next = updater(current)
      draftCacheRef.current[draftKey] = next
      return next
    })
  }

  const { draftValues, manualContent } = draftState

  const validation = useMemo(() => {
    if (analysis.state === 'unsupported') {
      const trimmed = manualContent.trim()
      if (trimmed === '') {
        return {
          content: {},
          error: '',
        }
      }
      try {
        const parsed = JSON.parse(trimmed)
        if (parsed === null) {
          return {
            content: {},
            error: 'Content cannot be null.',
          }
        }
        return {
          content: parsed,
          error: '',
        }
      } catch {
        return {
          content: {},
          error: 'Content must be valid JSON.',
        }
      }
    }
    if (analysis.state === 'empty') {
      return {
        content: {},
        error: '',
      }
    }
    return buildElicitationContent(analysis.node, draftValues)
  }, [analysis, draftValues, manualContent])

  const canAccept =
    !disabled && validation.error === '' && (analysis.state !== 'unsupported' || manualContent.trim().length > 0)
  const message =
    analysis.state === 'ready'
      ? 'Fill in the requested fields, then accept to send the structured response back to Codex.'
      : analysis.state === 'empty'
        ? 'This request only needs confirmation. Accept to continue or decline to stop.'
        : 'This request uses a schema shape that Maestro does not render yet. Paste the JSON payload to continue.'
  const requestDetails =
    analysis.state === 'ready'
      ? 'Complete the form below before accepting.'
      : analysis.state === 'empty'
        ? 'No structured fields were supplied with this request.'
        : analysis.reason

  return (
    <div className="grid gap-4">
      <div className="grid gap-4 rounded-[calc(var(--panel-radius)-0.2rem)] border border-white/10 bg-white/[0.03] p-4">
        <div className="space-y-1">
          <p className="text-sm font-medium text-white">Requested information</p>
          <p className="text-sm leading-6 text-[var(--muted-foreground)]">{requestDetails}</p>
        </div>
        <p className="text-sm leading-6 text-[var(--muted-foreground)]">{message}</p>
        {analysis.state === 'ready' ? (
          <SchemaNodeEditor
            disabled={disabled}
            isRoot
            node={analysis.node}
            path="root"
            value={draftValues}
            onChange={(nextValue) => {
              updateDraftState((current) => ({
                ...current,
                draftValues: nextValue,
              }))
            }}
          />
        ) : analysis.state === 'empty' ? (
          <div className="rounded-[calc(var(--panel-radius)-0.25rem)] border border-white/10 bg-black/20 px-4 py-4">
            <p className="text-sm leading-6 text-[var(--muted-foreground)]">
              Accepting will continue with an empty structured response.
            </p>
          </div>
        ) : (
          <div className="grid gap-3 rounded-[calc(var(--panel-radius)-0.25rem)] border border-amber-400/20 bg-amber-400/10 p-4">
            <div className="space-y-1">
              <p className="text-sm font-medium text-white">Manual JSON payload</p>
              <p className="text-sm leading-6 text-amber-100/90">
                Paste the JSON body that should be returned for this elicitation request.
              </p>
            </div>
            <Textarea
              className="min-h-[10rem] border-white/10 bg-black/20 text-white placeholder:text-white/30"
              disabled={disabled}
              placeholder='{"key":"value"}'
              value={manualContent}
              onChange={(event) => {
                const nextManualContent = event.target.value
                updateDraftState((current) => ({
                  ...current,
                  manualContent: nextManualContent,
                }))
              }}
            />
          </div>
        )}
        {validation.error ? (
          <p aria-live="polite" className="text-sm leading-6 text-rose-100">
            {validation.error}
          </p>
        ) : null}
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <Button
          className="h-11 rounded-2xl border px-4 text-sm font-medium transition border-[var(--accent)]/45 bg-[linear-gradient(135deg,rgba(196,255,87,.24),rgba(255,255,255,.06))] text-white hover:border-[var(--accent)]/60"
          disabled={!canAccept}
          type="button"
          onClick={() => {
            if (!canAccept) {
              return
            }
            onAccept(validation.content)
          }}
        >
          {isSubmitting ? 'Submitting...' : 'Accept and continue'}
        </Button>
        <Button
          className="h-11 rounded-2xl border-white/10 bg-white/5 px-4 text-sm font-medium text-white hover:border-white/20 hover:bg-white/8"
          disabled={disabled}
          type="button"
          variant="secondary"
          onClick={onDecline}
        >
          Decline
        </Button>
        <Button
          className="h-11 rounded-2xl border-white/10 bg-white/5 px-4 text-sm font-medium text-white hover:border-white/20 hover:bg-white/8"
          disabled={disabled}
          type="button"
          variant="secondary"
          onClick={onCancel}
        >
          Cancel
        </Button>
      </div>
    </div>
  )
}
