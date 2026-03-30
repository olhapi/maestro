import { toTitleCase } from '@/lib/utils'

export type ElicitationSchemaState =
  | { state: 'empty'; node: null }
  | { state: 'ready'; node: ElicitationSchemaNode }
  | { state: 'unsupported'; node: null; reason: string }

export type ElicitationFieldOption = {
  value: string
  label: string
}

export type ElicitationPrimitiveFieldType =
  | 'string'
  | 'number'
  | 'integer'
  | 'boolean'
  | 'single_select'
  | 'multi_select'

type ElicitationNodeBase = {
  label: string
  description: string
  required: boolean
  defaultValue?: unknown
}

export type ElicitationSchemaNode =
  | ElicitationPrimitiveNode
  | ElicitationObjectNode
  | ElicitationArrayNode
  | ElicitationUnionNode

export type ElicitationPrimitiveNode = ElicitationNodeBase & {
  kind: 'primitive'
  fieldType: ElicitationPrimitiveFieldType
  format?: 'email' | 'uri' | 'date' | 'date-time'
  options?: ElicitationFieldOption[]
  minLength?: number
  maxLength?: number
  minimum?: number
  maximum?: number
  minItems?: number
  maxItems?: number
}

export type ElicitationObjectProperty = {
  name: string
  label: string
  description: string
  required: boolean
  node: ElicitationSchemaNode
}

export type ElicitationObjectNode = ElicitationNodeBase & {
  kind: 'object'
  properties: ElicitationObjectProperty[]
  minProperties?: number
  maxProperties?: number
}

export type ElicitationArrayNode = ElicitationNodeBase & {
  kind: 'array'
  item: ElicitationSchemaNode
  minItems?: number
  maxItems?: number
}

export type ElicitationUnionOption = {
  label: string
  description: string
  node: ElicitationSchemaNode
}

export type ElicitationUnionNode = ElicitationNodeBase & {
  kind: 'union'
  mode: 'oneOf' | 'anyOf'
  options: ElicitationUnionOption[]
}

export interface ElicitationDraftObjectValue {
  [key: string]: ElicitationDraftValue
}

export interface ElicitationDraftUnionValue {
  kind: 'union'
  selectedIndex: number
  branches: ElicitationDraftValue[]
}

export type ElicitationDraftValue =
  | string
  | boolean
  | string[]
  | ElicitationDraftObjectValue
  | ElicitationDraftValue[]
  | ElicitationDraftUnionValue

type SchemaRecord = Record<string, unknown>
type ParseResult = { node: ElicitationSchemaNode } | { error: string }
type BuildResult = {
  content: unknown
  error: string
  present: boolean
}
type RequiredNamesResult = {
  error: string | null
  required: Set<string>
}

type ObjectConstraintOverlay = {
  required: string[]
  minProperties?: number
  maxProperties?: number
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' && !Array.isArray(value)
}

function isNonNegativeInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isInteger(value) && value >= 0
}

function isFiniteNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value)
}

function hasShapeKeys(schema: SchemaRecord) {
  return (
    'type' in schema ||
    'properties' in schema ||
    'items' in schema ||
    'enum' in schema ||
    'const' in schema ||
    'oneOf' in schema ||
    'anyOf' in schema ||
    'allOf' in schema ||
    'required' in schema ||
    'minProperties' in schema ||
    'maxProperties' in schema
  )
}

function hasRenderableShapeKeys(schema: SchemaRecord) {
  const declaredType = typeof schema.type === 'string' ? schema.type : ''
  return (
    isPlainObject(schema.properties) ||
    'items' in schema ||
    'enum' in schema ||
    'const' in schema ||
    'oneOf' in schema ||
    'anyOf' in schema ||
    'allOf' in schema ||
    declaredType === 'string' ||
    declaredType === 'number' ||
    declaredType === 'integer' ||
    declaredType === 'boolean'
  )
}

function parseObjectConstraintOverlay(
  schema: SchemaRecord,
  label: string,
): { overlay: ObjectConstraintOverlay | null; error: string | null } {
  const overlay: ObjectConstraintOverlay = { required: [] }
  let hasOverlay = false

  if ('required' in schema) {
    if (!Array.isArray(schema.required)) {
      return {
        overlay: null,
        error: `${label} uses an invalid required list.`,
      }
    }

    const required: string[] = []
    for (const item of schema.required) {
      if (typeof item !== 'string' || !item.trim()) {
        return {
          overlay: null,
          error: `${label} uses an invalid required list.`,
        }
      }
      required.push(item)
    }
    if (required.length > 0) {
      overlay.required = required
      hasOverlay = true
    }
  }

  if ('minProperties' in schema) {
    const minProperties = toConstraintInteger(schema.minProperties)
    if (schema.minProperties !== undefined && minProperties === null) {
      return {
        overlay: null,
        error: `${label} uses invalid object constraints.`,
      }
    }
    if (typeof minProperties === 'number') {
      overlay.minProperties = minProperties
      hasOverlay = true
    }
  }

  if ('maxProperties' in schema) {
    const maxProperties = toConstraintInteger(schema.maxProperties)
    if (schema.maxProperties !== undefined && maxProperties === null) {
      return {
        overlay: null,
        error: `${label} uses invalid object constraints.`,
      }
    }
    if (typeof maxProperties === 'number') {
      overlay.maxProperties = maxProperties
      hasOverlay = true
    }
  }

  if (!hasOverlay) {
    return { overlay: null, error: null }
  }

  return { overlay, error: null }
}

function applyObjectConstraintOverlay(
  node: ElicitationObjectNode,
  overlay: ObjectConstraintOverlay,
  label: string,
): { node: ElicitationObjectNode } | { error: string } {
  const next: ElicitationObjectNode = {
    ...node,
    properties: node.properties.map((property) => ({ ...property })),
  }
  const propertyIndexes = new Map<string, number>()
  for (const [index, property] of next.properties.entries()) {
    propertyIndexes.set(property.name, index)
  }

  for (const requiredName of overlay.required) {
    const propertyIndex = propertyIndexes.get(requiredName)
    if (propertyIndex === undefined) {
      return {
        error: `${label} requires an unknown field named "${requiredName}".`,
      }
    }
    next.properties[propertyIndex] = {
      ...next.properties[propertyIndex],
      required: true,
    }
  }

  if (typeof overlay.minProperties === 'number') {
    next.minProperties =
      typeof next.minProperties === 'number'
        ? Math.max(next.minProperties, overlay.minProperties)
        : overlay.minProperties
  }
  if (typeof overlay.maxProperties === 'number') {
    next.maxProperties =
      typeof next.maxProperties === 'number'
        ? Math.min(next.maxProperties, overlay.maxProperties)
        : overlay.maxProperties
  }

  if (
    typeof next.minProperties === 'number' &&
    typeof next.maxProperties === 'number' &&
    next.minProperties > next.maxProperties
  ) {
    return {
      error: `${label} has conflicting object constraints.`,
    }
  }

  return { node: next }
}

function toFieldLabel(name: string, rawTitle: unknown) {
  if (typeof rawTitle === 'string' && rawTitle.trim()) {
    return rawTitle.trim()
  }
  return toTitleCase(name.replace(/[_-]+/g, ' '))
}

function toConstraintInteger(value: unknown): number | null {
  if (value === undefined || value === null) {
    return null
  }
  if (isNonNegativeInteger(value)) {
    return value
  }
  return null
}

function toConstraintNumber(value: unknown): number | null {
  if (value === undefined || value === null) {
    return null
  }
  if (isFiniteNumber(value)) {
    return value
  }
  return null
}

function sameJsonValue(left: unknown, right: unknown) {
  return JSON.stringify(left) === JSON.stringify(right)
}

function isValidDateString(value: string) {
	if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) {
		return false
	}

  const [yearText, monthText, dayText] = value.split('-')
  const year = Number(yearText)
  const month = Number(monthText)
  const day = Number(dayText)
  const parsed = new Date(Date.UTC(year, month - 1, day))

	return (
		parsed.getUTCFullYear() === year &&
		parsed.getUTCMonth() === month - 1 &&
		parsed.getUTCDate() === day
	)
}

function isLocalDateTimeString(value: string) {
	return /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?$/.test(value)
}

function toLocalDateTimeInputValue(value: string) {
	const trimmed = value.trim()
	if (trimmed === '' || isLocalDateTimeString(trimmed)) {
		return trimmed
	}

	const parsed = new Date(trimmed)
	if (Number.isNaN(parsed.getTime())) {
		return trimmed
	}

	const year = parsed.getFullYear()
	const month = String(parsed.getMonth() + 1).padStart(2, '0')
	const day = String(parsed.getDate()).padStart(2, '0')
	const hours = String(parsed.getHours()).padStart(2, '0')
	const minutes = String(parsed.getMinutes()).padStart(2, '0')

	return `${year}-${month}-${day}T${hours}:${minutes}`
}

function normalizeDateTimeValue(value: string) {
	const trimmed = value.trim()
	if (trimmed === '') {
		return { normalized: '', valid: false }
	}
	if (isLocalDateTimeString(trimmed)) {
		const parsed = new Date(trimmed)
		if (Number.isNaN(parsed.getTime())) {
			return { normalized: '', valid: false }
		}
		return { normalized: parsed.toISOString(), valid: true }
	}
	if (Number.isNaN(Date.parse(trimmed))) {
		return { normalized: '', valid: false }
	}
	return { normalized: trimmed, valid: true }
}

function mergeText(left: string, right: string) {
  if (!left) {
    return right
  }
  if (!right || left === right) {
    return left
  }
  return left
}

function combineLabel(parent: string, child: string) {
  return parent ? `${parent} / ${child}` : child
}

function getSchemaDescription(schema: SchemaRecord) {
  return typeof schema.description === 'string' ? schema.description.trim() : ''
}

function getSchemaDefaultValue(schema: SchemaRecord) {
  return schema.default
}

function cloneSchemaWithoutKeys(schema: SchemaRecord, keys: string[]) {
  const next: SchemaRecord = {}
  for (const [key, value] of Object.entries(schema)) {
    if (!keys.includes(key)) {
      next[key] = value
    }
  }
  return next
}

function parseEnumOptions(
  schema: SchemaRecord,
  unsupportedLabel: string,
): { options: ElicitationFieldOption[]; matched: boolean; error?: string } {
  if (Array.isArray(schema.enum)) {
    const enumNames = Array.isArray(schema.enumNames) ? schema.enumNames : []
    const options: ElicitationFieldOption[] = []

    for (const [index, option] of schema.enum.entries()) {
      if (typeof option !== 'string') {
        return {
          matched: true,
          options: [],
          error: `${unsupportedLabel} uses an unsupported enum value.`,
        }
      }
      options.push({
        value: option,
        label:
          typeof enumNames[index] === 'string' && enumNames[index].trim()
            ? enumNames[index].trim()
            : option,
      })
    }

    if (options.length === 0) {
      return {
        matched: true,
        options: [],
        error: `${unsupportedLabel} does not define any enum options.`,
      }
    }

    return { matched: true, options }
  }

  const branchSets = [schema.oneOf, schema.anyOf]
  for (const branchSet of branchSets) {
    if (!Array.isArray(branchSet)) {
      continue
    }

    const options: ElicitationFieldOption[] = []
    for (const branch of branchSet) {
      if (!isPlainObject(branch)) {
        return { matched: false, options: [] }
      }
      if (!('const' in branch)) {
        return { matched: false, options: [] }
      }
      if (typeof branch.const !== 'string') {
        return {
          matched: true,
          options: [],
          error: `${unsupportedLabel} uses an unsupported enum option shape.`,
        }
      }
      options.push({
        value: branch.const,
        label:
          typeof branch.title === 'string' && branch.title.trim() ? branch.title.trim() : branch.const,
      })
    }

    if (options.length === 0) {
      return {
        matched: true,
        options: [],
        error: `${unsupportedLabel} does not define any enum options.`,
      }
    }

    return { matched: true, options }
  }

  return { matched: false, options: [] }
}

function getRequiredNames(
  rawRequired: unknown,
  propertyNames: Set<string>,
  label: string,
): RequiredNamesResult {
  if (rawRequired === undefined) {
    return { error: null, required: new Set<string>() }
  }
  if (!Array.isArray(rawRequired)) {
    return {
      error: `${label} uses an invalid required list.`,
      required: new Set<string>(),
    }
  }

  const next = new Set<string>()
  for (const item of rawRequired) {
    if (typeof item !== 'string' || !item.trim()) {
      return {
        error: `${label} uses an invalid required list.`,
        required: new Set<string>(),
      }
    }
    if (!propertyNames.has(item)) {
      return {
        error: `${label} requires an unknown field named "${item}".`,
        required: new Set<string>(),
      }
    }
    next.add(item)
  }

  return { error: null, required: next }
}

function normalizeMetadataNode(schema: SchemaRecord, label: string, required: boolean): ElicitationNodeBase {
  return {
    label,
    description: getSchemaDescription(schema),
    required,
    defaultValue: getSchemaDefaultValue(schema),
  }
}

function normalizePrimitiveSchema(schema: SchemaRecord, label: string, required: boolean): ParseResult {
  const selectOptions = parseEnumOptions(schema, label)
  if (selectOptions.error) {
    return { error: selectOptions.error }
  }

  const declaredType = typeof schema.type === 'string' ? schema.type : ''

  if (selectOptions.matched) {
    return {
      node: {
        kind: 'primitive',
        fieldType: 'single_select',
        ...normalizeMetadataNode(schema, label, required),
        options: selectOptions.options,
      },
    }
  }

  if (declaredType === 'boolean') {
    return {
      node: {
        kind: 'primitive',
        fieldType: 'boolean',
        ...normalizeMetadataNode(schema, label, required),
      },
    }
  }

  if (declaredType === 'number' || declaredType === 'integer') {
    const minimum = toConstraintNumber(schema.minimum)
    const maximum = toConstraintNumber(schema.maximum)
    if (
      (schema.minimum !== undefined && minimum === null) ||
      (schema.maximum !== undefined && maximum === null)
    ) {
      return { error: `${label} uses invalid numeric constraints.` }
    }

    return {
      node: {
        kind: 'primitive',
        fieldType: declaredType,
        ...normalizeMetadataNode(schema, label, required),
        minimum: minimum ?? undefined,
        maximum: maximum ?? undefined,
      },
    }
  }

  if (declaredType === 'string') {
    const minLength = toConstraintInteger(schema.minLength)
    const maxLength = toConstraintInteger(schema.maxLength)
    if (
      (schema.minLength !== undefined && minLength === null) ||
      (schema.maxLength !== undefined && maxLength === null)
    ) {
      return { error: `${label} uses invalid string length constraints.` }
    }

    const format =
      typeof schema.format === 'string'
        ? (schema.format as ElicitationPrimitiveNode['format'])
        : undefined
    if (format && !['email', 'uri', 'date', 'date-time'].includes(format)) {
      return { error: `${label} uses an unsupported string format.` }
    }

    return {
      node: {
        kind: 'primitive',
        fieldType: 'string',
        ...normalizeMetadataNode(schema, label, required),
        format,
        minLength: minLength ?? undefined,
        maxLength: maxLength ?? undefined,
      },
    }
  }

  return { error: `${label} uses an unsupported schema type.` }
}

function normalizeSelectLikeSchema(
  schema: SchemaRecord,
  label: string,
  required: boolean,
): ParseResult | null {
  const selectOptions = parseEnumOptions(schema, label)
  if (selectOptions.error) {
    return { error: selectOptions.error }
  }
  if (!selectOptions.matched) {
    return null
  }

  return {
    node: {
      kind: 'primitive',
      fieldType: 'single_select',
      ...normalizeMetadataNode(schema, label, required),
      options: selectOptions.options,
    },
  }
}

function normalizeArraySchema(schema: SchemaRecord, label: string, required: boolean): ParseResult {
  if (!('items' in schema)) {
    return { error: `${label} uses an unsupported array schema shape.` }
  }
  if (Array.isArray(schema.items)) {
    return { error: `${label} uses tuple arrays, which are not supported.` }
  }
  if (!isPlainObject(schema.items)) {
    return { error: `${label} uses an unsupported array item shape.` }
  }
  const itemsSchema = schema.items as SchemaRecord

  const minItems = toConstraintInteger(schema.minItems)
  const maxItems = toConstraintInteger(schema.maxItems)
  if (
    (schema.minItems !== undefined && minItems === null) ||
    (schema.maxItems !== undefined && maxItems === null)
  ) {
    return { error: `${label} uses invalid array constraints.` }
  }

  const itemSelectLike = normalizeSelectLikeSchema(
    itemsSchema,
    label,
    false,
  )
  if (itemSelectLike && 'error' in itemSelectLike) {
    return { error: itemSelectLike.error }
  }
  const itemDeclaredType = typeof itemsSchema.type === 'string' ? itemsSchema.type : ''
  if (itemSelectLike && 'node' in itemSelectLike && itemSelectLike.node.kind === 'primitive') {
    if (itemDeclaredType && itemDeclaredType !== 'string') {
      return { error: `${label} uses an unsupported array item shape.` }
    }
    return {
      node: {
        kind: 'primitive',
        fieldType: 'multi_select',
        ...normalizeMetadataNode(schema, label, required),
        options: itemSelectLike.node.options,
        minItems: minItems ?? undefined,
        maxItems: maxItems ?? undefined,
      },
    }
  }

  const itemResult = normalizeSchemaNode(itemsSchema, `${label} item`, false)
  if ('error' in itemResult) {
    return { error: itemResult.error }
  }

  return {
    node: {
      kind: 'array',
      ...normalizeMetadataNode(schema, label, required),
      item: itemResult.node,
      minItems: minItems ?? undefined,
      maxItems: maxItems ?? undefined,
    },
  }
}

function normalizeObjectSchema(schema: SchemaRecord, label: string, required: boolean): ParseResult {
  if (!isPlainObject(schema.properties)) {
    return { error: `${label} uses an unsupported object schema shape.` }
  }

  const propertyNames = new Set(Object.keys(schema.properties))
  const requiredSet = getRequiredNames(schema.required, propertyNames, label)
  if (requiredSet.error) {
    return { error: requiredSet.error }
  }

  const minProperties = toConstraintInteger(schema.minProperties)
  const maxProperties = toConstraintInteger(schema.maxProperties)
  if (
    (schema.minProperties !== undefined && minProperties === null) ||
    (schema.maxProperties !== undefined && maxProperties === null)
  ) {
    return { error: `${label} uses invalid object constraints.` }
  }
  if (
    typeof minProperties === 'number' &&
    typeof maxProperties === 'number' &&
    minProperties > maxProperties
  ) {
    return { error: `${label} has conflicting object constraints.` }
  }

  const properties: ElicitationObjectProperty[] = []
  for (const [name, rawPropertySchema] of Object.entries(schema.properties)) {
    const propertyLabel = toFieldLabel(
      name,
      isPlainObject(rawPropertySchema) ? rawPropertySchema.title : undefined,
    )
    const propertyDescription =
      isPlainObject(rawPropertySchema) && typeof rawPropertySchema.description === 'string'
        ? rawPropertySchema.description.trim()
        : ''
    const propertyRequired = requiredSet.required.has(name)

    const propertyResult = normalizeSchemaNode(rawPropertySchema, propertyLabel, propertyRequired)
    if ('error' in propertyResult) {
      return { error: propertyResult.error }
    }

    properties.push({
      name,
      label: propertyLabel,
      description: propertyDescription,
      required: propertyRequired,
      node: propertyResult.node,
    })
  }

  return {
    node: {
      kind: 'object',
      ...normalizeMetadataNode(schema, label, required),
      properties,
      minProperties: minProperties ?? undefined,
      maxProperties: maxProperties ?? undefined,
    },
  }
}

function requiresManualObjectContent(schema: unknown) {
  if (!isPlainObject(schema)) {
    return false
  }

  if (typeof schema.minProperties === 'number' && schema.minProperties > 0) {
    return true
  }

  if ('additionalProperties' in schema && schema.additionalProperties !== false) {
    return true
  }

  if (isPlainObject(schema.patternProperties) && Object.keys(schema.patternProperties).length > 0) {
    return true
  }

  return 'propertyNames' in schema || 'unevaluatedProperties' in schema
}

function mergePrimitiveNodes(left: ElicitationPrimitiveNode, right: ElicitationPrimitiveNode): ParseResult {
  const label = left.label || right.label
  const description = mergeText(left.description, right.description)
  const required = left.required || right.required
  const defaultValue = left.defaultValue ?? right.defaultValue
  const mergedBase: ElicitationPrimitiveNode = {
    ...left,
    label,
    description,
    required,
    defaultValue,
  }

  if (left.fieldType === right.fieldType) {
    if (left.fieldType === 'single_select') {
      const optionMap = new Map(right.options?.map((option) => [option.value, option] as const))
      const options =
        left.options
          ?.filter((option) => optionMap.has(option.value))
          .map((option) => {
            const matched = optionMap.get(option.value)!
            return {
              value: option.value,
              label: option.label || matched.label || option.value,
            }
          }) ?? []

      if (options.length === 0) {
        return { error: `${label} has conflicting enum options.` }
      }

      return {
        node: {
          ...mergedBase,
          fieldType: 'single_select',
          options,
        },
      }
    }

    if (left.fieldType === 'multi_select') {
      const optionMap = new Map(right.options?.map((option) => [option.value, option] as const))
      const options =
        left.options
          ?.filter((option) => optionMap.has(option.value))
          .map((option) => {
            const matched = optionMap.get(option.value)!
            return {
              value: option.value,
              label: option.label || matched.label || option.value,
            }
          }) ?? []

      if (options.length === 0) {
        return { error: `${label} has conflicting enum options.` }
      }

      const minItems = Math.max(left.minItems ?? 0, right.minItems ?? 0) || undefined
      const maxItems =
        left.maxItems === undefined
          ? right.maxItems
          : right.maxItems === undefined
            ? left.maxItems
            : Math.min(left.maxItems, right.maxItems)
      if (typeof minItems === 'number' && typeof maxItems === 'number' && minItems > maxItems) {
        return { error: `${label} has conflicting multi-select constraints.` }
      }

      return {
        node: {
          ...mergedBase,
          fieldType: 'multi_select',
          options,
          minItems,
          maxItems,
        },
      }
    }

    if (left.fieldType === 'string') {
      const minLength = Math.max(left.minLength ?? 0, right.minLength ?? 0) || undefined
      const maxLength =
        left.maxLength === undefined
          ? right.maxLength
          : right.maxLength === undefined
            ? left.maxLength
            : Math.min(left.maxLength, right.maxLength)
      if (typeof minLength === 'number' && typeof maxLength === 'number' && minLength > maxLength) {
        return { error: `${label} has conflicting string length constraints.` }
      }

      const leftFormat = left.format
      const rightFormat = right.format
      if (leftFormat && rightFormat && leftFormat !== rightFormat) {
        return { error: `${label} has conflicting string formats.` }
      }

      return {
        node: {
          ...mergedBase,
          fieldType: 'string',
          format: leftFormat ?? rightFormat,
          minLength,
          maxLength,
        },
      }
    }

    if (left.fieldType === 'number' || left.fieldType === 'integer') {
      const minimum =
        left.minimum === undefined
          ? right.minimum
          : right.minimum === undefined
            ? left.minimum
            : Math.max(left.minimum, right.minimum)
      const maximum =
        left.maximum === undefined
          ? right.maximum
          : right.maximum === undefined
            ? left.maximum
            : Math.min(left.maximum, right.maximum)
      if (
        typeof minimum === 'number' &&
        typeof maximum === 'number' &&
        minimum > maximum
      ) {
        return { error: `${label} has conflicting numeric constraints.` }
      }

      return {
        node: {
          ...mergedBase,
          fieldType: left.fieldType === 'integer' || right.fieldType === 'integer' ? 'integer' : 'number',
          minimum,
          maximum,
        },
      }
    }

    if (left.fieldType === 'boolean') {
      if (
        left.defaultValue !== undefined &&
        right.defaultValue !== undefined &&
        !sameJsonValue(left.defaultValue, right.defaultValue)
      ) {
        return { error: `${label} has conflicting defaults.` }
      }
      return { node: mergedBase }
    }
  }

  if (
    left.fieldType === 'string' &&
    right.fieldType === 'single_select'
  ) {
    return mergePrimitiveNodes(
      {
        ...left,
        fieldType: 'single_select',
        options: right.options,
      },
      right,
    )
  }

  if (
    left.fieldType === 'single_select' &&
    right.fieldType === 'string'
  ) {
    return mergePrimitiveNodes(left, {
      ...right,
      fieldType: 'single_select',
      options: left.options,
    })
  }

  if (
    left.fieldType === 'integer' &&
    right.fieldType === 'number'
  ) {
    return mergePrimitiveNodes(left, { ...right, fieldType: 'integer' })
  }

  if (
    left.fieldType === 'number' &&
    right.fieldType === 'integer'
  ) {
    return mergePrimitiveNodes({ ...left, fieldType: 'integer' }, right)
  }

  return { error: `${label} has conflicting primitive shapes.` }
}

function mergeObjectNodes(left: ElicitationObjectNode, right: ElicitationObjectNode): ParseResult {
  const properties = new Map<string, ElicitationObjectProperty>()

  for (const property of left.properties) {
    properties.set(property.name, {
      ...property,
    })
  }

  for (const property of right.properties) {
    const existing = properties.get(property.name)
    if (!existing) {
      properties.set(property.name, {
        ...property,
      })
      continue
    }

    const mergedNode = mergeSchemaNodes(existing.node, property.node)
    if ('error' in mergedNode) {
      return { error: mergedNode.error }
    }

    properties.set(property.name, {
      name: property.name,
      label: existing.label || property.label,
      description: mergeText(existing.description, property.description),
      required: existing.required || property.required,
      node: mergedNode.node,
    })
  }

  const minProperties =
    left.minProperties === undefined
      ? right.minProperties
      : right.minProperties === undefined
        ? left.minProperties
        : Math.max(left.minProperties, right.minProperties)
  const maxProperties =
    left.maxProperties === undefined
      ? right.maxProperties
      : right.maxProperties === undefined
        ? left.maxProperties
        : Math.min(left.maxProperties, right.maxProperties)
  if (
    typeof minProperties === 'number' &&
    typeof maxProperties === 'number' &&
    minProperties > maxProperties
  ) {
    return { error: `${left.label || right.label} has conflicting object constraints.` }
  }

  return {
    node: {
      ...left,
      label: left.label || right.label,
      description: mergeText(left.description, right.description),
      required: left.required || right.required,
      defaultValue: left.defaultValue ?? right.defaultValue,
      properties: [...properties.values()],
      minProperties,
      maxProperties,
    },
  }
}

function mergeArrayNodes(left: ElicitationArrayNode, right: ElicitationArrayNode): ParseResult {
  const itemResult = mergeSchemaNodes(left.item, right.item)
  if ('error' in itemResult) {
    return { error: itemResult.error }
  }

  const minItems = Math.max(left.minItems ?? 0, right.minItems ?? 0) || undefined
  const maxItems =
    left.maxItems === undefined
      ? right.maxItems
      : right.maxItems === undefined
        ? left.maxItems
        : Math.min(left.maxItems, right.maxItems)
  if (typeof minItems === 'number' && typeof maxItems === 'number' && minItems > maxItems) {
    return { error: `${left.label || right.label} has conflicting array constraints.` }
  }

  if (
    left.defaultValue !== undefined &&
    right.defaultValue !== undefined &&
    !sameJsonValue(left.defaultValue, right.defaultValue)
  ) {
    return { error: `${left.label || right.label} has conflicting defaults.` }
  }

  return {
    node: {
      ...left,
      label: left.label || right.label,
      description: mergeText(left.description, right.description),
      required: left.required || right.required,
      defaultValue: left.defaultValue ?? right.defaultValue,
      item: itemResult.node,
      minItems,
      maxItems,
    },
  }
}

function mergeUnionNodes(left: ElicitationUnionNode, right: ElicitationUnionNode): ParseResult {
  if (left.mode !== right.mode || left.options.length !== right.options.length) {
    return { error: `${left.label || right.label} has conflicting composed branches.` }
  }

  const options: ElicitationUnionOption[] = []
  for (let index = 0; index < left.options.length; index += 1) {
    const leftOption = left.options[index]
    const rightOption = right.options[index]
    const merged = mergeSchemaNodes(leftOption.node, rightOption.node)
    if ('error' in merged) {
      return { error: merged.error }
    }
    options.push({
      label: leftOption.label || rightOption.label,
      description: mergeText(leftOption.description, rightOption.description),
      node: merged.node,
    })
  }

  return {
    node: {
      ...left,
      label: left.label || right.label,
      description: mergeText(left.description, right.description),
      required: left.required || right.required,
      defaultValue: left.defaultValue ?? right.defaultValue,
      options,
    },
  }
}

function mergeSchemaNodes(left: ElicitationSchemaNode, right: ElicitationSchemaNode): ParseResult {
  if (left.kind === 'primitive' && right.kind === 'primitive') {
    return mergePrimitiveNodes(left, right)
  }
  if (left.kind === 'object' && right.kind === 'object') {
    return mergeObjectNodes(left, right)
  }
  if (left.kind === 'array' && right.kind === 'array') {
    return mergeArrayNodes(left, right)
  }
  if (left.kind === 'union' && right.kind === 'union') {
    return mergeUnionNodes(left, right)
  }

  if (
    left.kind === 'primitive' &&
    right.kind === 'primitive' &&
    left.fieldType === 'string' &&
    right.fieldType === 'single_select'
  ) {
    return mergePrimitiveNodes(
      {
        ...left,
        fieldType: 'single_select',
        options: right.options,
      },
      right,
    )
  }

  if (
    left.kind === 'primitive' &&
    right.kind === 'primitive' &&
    left.fieldType === 'single_select' &&
    right.fieldType === 'string'
  ) {
    return mergePrimitiveNodes(left, {
      ...right,
      fieldType: 'single_select',
      options: left.options,
    })
  }

  if (
    left.kind === 'primitive' &&
    right.kind === 'primitive' &&
    left.fieldType === 'integer' &&
    right.fieldType === 'number'
  ) {
    return mergePrimitiveNodes(left, { ...right, fieldType: 'integer' })
  }

  if (
    left.kind === 'primitive' &&
    right.kind === 'primitive' &&
    left.fieldType === 'number' &&
    right.fieldType === 'integer'
  ) {
    return mergePrimitiveNodes({ ...left, fieldType: 'integer' }, right)
  }

  return { error: `${left.label || right.label} has conflicting schema shapes.` }
}

function normalizeAllOfSchema(schema: SchemaRecord, label: string, required: boolean): ParseResult {
  const branches = Array.isArray(schema.allOf) ? schema.allOf : []
  if (branches.length === 0) {
    return { error: `${label} uses an unsupported allOf schema shape.` }
  }

  const baseSchema = cloneSchemaWithoutKeys(schema, ['allOf'])
  const overlays: ObjectConstraintOverlay[] = []
  let merged: ElicitationSchemaNode | null = null

  if (hasShapeKeys(baseSchema)) {
    const baseResult = normalizeSchemaNode(baseSchema, label, required)
    if ('error' in baseResult) {
      if (hasRenderableShapeKeys(baseSchema)) {
        return { error: baseResult.error }
      }
      const baseOverlay = parseObjectConstraintOverlay(baseSchema, label)
      if (baseOverlay.error) {
        return { error: baseOverlay.error }
      }
      if (baseOverlay.overlay) {
        overlays.push(baseOverlay.overlay)
      } else {
        return { error: baseResult.error }
      }
    } else {
      merged = baseResult.node
    }
  }

  for (const branch of branches) {
    if (!isPlainObject(branch) || !hasShapeKeys(branch)) {
      continue
    }
    const branchResult = normalizeSchemaNode(branch, label, required)
    if ('error' in branchResult) {
      if (hasRenderableShapeKeys(branch)) {
        return { error: branchResult.error }
      }
      const branchOverlay = parseObjectConstraintOverlay(branch, label)
      if (branchOverlay.error) {
        return { error: branchOverlay.error }
      }
      if (branchOverlay.overlay) {
        overlays.push(branchOverlay.overlay)
        continue
      }
      return { error: branchResult.error }
    }
    if (!merged) {
      merged = branchResult.node
      continue
    }

    const mergedResult = mergeSchemaNodes(merged, branchResult.node)
    if ('error' in mergedResult) {
      return { error: mergedResult.error }
    }
    merged = mergedResult.node
  }

  if (!merged) {
    return { error: `${label} uses an unsupported allOf schema shape.` }
  }

  if (overlays.length > 0) {
    if (merged.kind !== 'object') {
      return { error: `${label} uses an unsupported allOf schema shape.` }
    }

    let objectNode: ElicitationObjectNode = merged
    for (const overlay of overlays) {
      const applied = applyObjectConstraintOverlay(objectNode, overlay, label)
      if ('error' in applied) {
        return { error: applied.error }
      }
      objectNode = applied.node
    }
    merged = objectNode
  }

  return { node: merged }
}

function normalizeComposedSchema(schema: SchemaRecord, label: string, required: boolean): ParseResult {
  const branchKey = Array.isArray(schema.oneOf) ? 'oneOf' : Array.isArray(schema.anyOf) ? 'anyOf' : ''
  const branches = branchKey ? (schema[branchKey] as unknown[]) : []
  if (branchKey === '') {
    return { error: `${label} uses an unsupported composed schema shape.` }
  }
  if (Array.isArray(schema.oneOf) && Array.isArray(schema.anyOf)) {
    return { error: `${label} uses incompatible composed schema branches.` }
  }
  if (branches.length === 0) {
    return { error: `${label} uses an unsupported composed schema shape.` }
  }

  const baseSchema = cloneSchemaWithoutKeys(schema, [branchKey])
  const baseOverlays: ObjectConstraintOverlay[] = []
  let baseResult: ParseResult | null = null
  if (hasShapeKeys(baseSchema)) {
    const parsedBase = normalizeSchemaNode(baseSchema, label, required)
    if ('error' in parsedBase) {
      if (hasRenderableShapeKeys(baseSchema)) {
        return { error: parsedBase.error }
      }
      const baseOverlay = parseObjectConstraintOverlay(baseSchema, label)
      if (baseOverlay.error) {
        return { error: baseOverlay.error }
      }
      if (baseOverlay.overlay) {
        baseOverlays.push(baseOverlay.overlay)
      } else {
        return { error: parsedBase.error }
      }
    } else {
      baseResult = parsedBase
    }
  }

  const selectLike = parseEnumOptions(schema, label)
  if (selectLike.error) {
    return { error: selectLike.error }
  }

  if (selectLike.matched) {
    const primitiveResult = normalizePrimitiveSchema(
      {
        ...schema,
        type: typeof schema.type === 'string' ? schema.type : 'string',
        enum: schema.enum,
        oneOf: schema.oneOf,
        anyOf: schema.anyOf,
      },
      label,
      required,
    )
    if ('error' in primitiveResult) {
      return { error: primitiveResult.error }
    }

    if (baseOverlays.length > 0) {
      return { error: `${label} uses an unsupported composed schema shape.` }
    }

    if (baseResult && 'node' in baseResult) {
      const merged = mergeSchemaNodes(baseResult.node, primitiveResult.node)
      if ('error' in merged) {
        return { error: merged.error }
      }
      return { node: merged.node }
    }

    return primitiveResult
  }

  const optionNodes: ElicitationUnionOption[] = []
  for (const [index, branch] of branches.entries()) {
    if (!isPlainObject(branch)) {
      return { error: `${label} uses an unsupported composed branch shape.` }
    }
    const branchLabel = toFieldLabel(
      `option-${index + 1}`,
      typeof branch.title === 'string' && branch.title.trim()
        ? branch.title.trim()
        : `Option ${index + 1}`,
    )
    const branchResult = normalizeSchemaNode(branch, branchLabel, false)
    if ('error' in branchResult) {
      return { error: branchResult.error }
    }
    optionNodes.push({
      label: branchLabel,
      description: getSchemaDescription(branch),
      node: branchResult.node,
    })
  }

  if (baseResult && 'node' in baseResult) {
    const mergedOptions: ElicitationUnionOption[] = []
    for (const option of optionNodes) {
      const merged = mergeSchemaNodes(baseResult.node, option.node)
      if ('error' in merged) {
        return { error: merged.error }
      }
      mergedOptions.push({
        ...option,
        node: merged.node,
      })
    }
    optionNodes.splice(0, optionNodes.length, ...mergedOptions)
  }

  if (baseOverlays.length > 0) {
    const mergedOptions: ElicitationUnionOption[] = []
    for (const option of optionNodes) {
      if (option.node.kind !== 'object') {
        return { error: `${label} uses an unsupported composed schema shape.` }
      }

      let objectNode: ElicitationObjectNode = option.node
      for (const overlay of baseOverlays) {
        const applied = applyObjectConstraintOverlay(objectNode, overlay, label)
        if ('error' in applied) {
          return { error: applied.error }
        }
        objectNode = applied.node
      }
      mergedOptions.push({
        ...option,
        node: objectNode,
      })
    }
    optionNodes.splice(0, optionNodes.length, ...mergedOptions)
  }

  if (optionNodes.length === 1) {
    return { node: optionNodes[0].node }
  }

  return {
    node: {
      kind: 'union',
      ...normalizeMetadataNode(schema, label, required),
      mode: branchKey as 'oneOf' | 'anyOf',
      options: optionNodes,
    },
  }
}

function normalizeSchemaNode(schema: unknown, label: string, required: boolean): ParseResult {
  if (!isPlainObject(schema)) {
    return { error: `${label} uses an unsupported schema shape.` }
  }

  if ('$ref' in schema) {
    return { error: `${label} uses unsupported schema references.` }
  }

  if (Array.isArray(schema.allOf)) {
    return normalizeAllOfSchema(schema, label, required)
  }

  if (Array.isArray(schema.oneOf) || Array.isArray(schema.anyOf)) {
    return normalizeComposedSchema(schema, label, required)
  }

  const hasProperties = isPlainObject(schema.properties)
  const hasItems = 'items' in schema
  const declaredType = typeof schema.type === 'string' ? schema.type : ''

  if (hasProperties || declaredType === 'object') {
    if (hasItems || declaredType === 'array') {
      return { error: `${label} mixes object and array schema shapes.` }
    }
    if (parseEnumOptions(schema, label).matched) {
      return { error: `${label} mixes object and enum schema shapes.` }
    }
    return normalizeObjectSchema(schema, label, required)
  }

  if (hasItems || declaredType === 'array') {
    if (hasProperties || declaredType === 'object') {
      return { error: `${label} mixes object and array schema shapes.` }
    }
    return normalizeArraySchema(schema, label, required)
  }

  if (Array.isArray(schema.oneOf) || Array.isArray(schema.anyOf)) {
    return normalizeComposedSchema(schema, label, required)
  }

  const selectLike = parseEnumOptions(schema, label)
  if (selectLike.error) {
    return { error: selectLike.error }
  }
  if (selectLike.matched) {
    if (declaredType && declaredType !== 'string') {
      return { error: `${label} uses an unsupported enum shape.` }
    }
    return normalizePrimitiveSchema(schema, label, required)
  }

  if (declaredType === 'string' || declaredType === 'number' || declaredType === 'integer' || declaredType === 'boolean') {
    return normalizePrimitiveSchema(schema, label, required)
  }

  return { error: `${label} uses an unsupported schema shape.` }
}

function isMeaningfulDraftValue(node: ElicitationSchemaNode, draft: unknown): boolean {
	if (node.kind === 'primitive') {
		switch (node.fieldType) {
		case 'boolean':
			return typeof draft === 'boolean'
      case 'multi_select':
        return Array.isArray(draft) && draft.length > 0
      case 'single_select':
      case 'string':
      case 'number':
      case 'integer':
        return typeof draft === 'string' && draft.trim().length > 0
      default:
        return false
    }
	}

	if (node.kind === 'union') {
		if (
			!isPlainObject(draft) ||
			draft.kind !== 'union' ||
			typeof draft.selectedIndex !== 'number' ||
			!Array.isArray(draft.branches)
		) {
			return false
		}
		const option = node.options[draft.selectedIndex]
		if (!option) {
			return false
		}
		return isMeaningfulDraftValue(option.node, draft.branches[draft.selectedIndex])
	}

  if (node.kind === 'array') {
    return Array.isArray(draft) && draft.length > 0
  }

  if (node.kind === 'object') {
    if (!isPlainObject(draft)) {
      return false
    }
    return node.properties.some((property) => isMeaningfulDraftValue(property.node, draft[property.name]))
  }

  return false
}

function createInitialDraftFromPrimitive(node: ElicitationPrimitiveNode): ElicitationDraftValue {
	switch (node.fieldType) {
		case 'boolean':
			return typeof node.defaultValue === 'boolean' ? node.defaultValue : ''
    case 'multi_select':
      return Array.isArray(node.defaultValue)
        ? node.defaultValue.filter(
            (value): value is string =>
              typeof value === 'string' && (node.options?.some((option) => option.value === value) ?? false),
          )
        : []
    case 'single_select':
      return typeof node.defaultValue === 'string' && node.options?.some((option) => option.value === node.defaultValue)
        ? node.defaultValue
        : ''
    case 'number':
    case 'integer':
      return typeof node.defaultValue === 'number' && Number.isFinite(node.defaultValue)
        ? String(node.defaultValue)
        : ''
		case 'string':
		default:
			return typeof node.defaultValue === 'string'
				? node.format === 'date-time'
					? toLocalDateTimeInputValue(node.defaultValue)
					: node.defaultValue
				: node.defaultValue == null
					? ''
					: String(node.defaultValue)
	}
}

function chooseInitialUnionIndex(node: ElicitationUnionNode, drafts: ElicitationDraftValue[]) {
  const meaningfulIndex = node.options.findIndex((option, index) => isMeaningfulDraftValue(option.node, drafts[index]))
  return meaningfulIndex >= 0 ? meaningfulIndex : 0
}

export function normalizeElicitationRequestedSchema(schema: unknown): ElicitationSchemaState {
  if (!isPlainObject(schema)) {
    return {
      state: 'unsupported',
      node: null,
      reason: 'Requested schema is missing or malformed.',
    }
  }

  const rootResult = normalizeSchemaNode(schema, 'Requested information', true)
  if ('error' in rootResult) {
    return {
      state: 'unsupported',
      node: null,
      reason: rootResult.error,
    }
  }

  if (rootResult.node.kind === 'object' && rootResult.node.properties.length === 0) {
    if (requiresManualObjectContent(schema)) {
      return {
        state: 'unsupported',
        node: null,
        reason: 'Requested schema requires keyed JSON content that the form renderer does not support yet.',
      }
    }
    return {
      state: 'empty',
      node: null,
    }
  }

  return {
    state: 'ready',
    node: rootResult.node,
  }
}

function buildPrimitiveContent(
  node: ElicitationPrimitiveNode,
  draft: unknown,
  forcePresent: boolean,
  label: string,
): BuildResult {
	const text = typeof draft === 'string' ? draft : ''
	const trimmed = text.trim()

	if (node.fieldType === 'boolean') {
		if (typeof draft !== 'boolean') {
			if (typeof node.defaultValue === 'boolean') {
				return {
					content: node.defaultValue,
					error: '',
					present: true,
				}
			}
			if (!forcePresent && !node.required) {
				return {
					content: false,
					error: '',
					present: false,
				}
			}
		}
		return {
			content: typeof draft === 'boolean' ? draft : false,
			error: '',
			present: true,
		}
	}

  if (node.fieldType === 'multi_select') {
    const selected = Array.from(
      new Set(Array.isArray(draft) ? draft.filter((value): value is string => typeof value === 'string') : []),
    ).filter((value) => node.options?.some((option) => option.value === value))

    if (selected.length === 0) {
      if (node.required || (typeof node.minItems === 'number' && node.minItems > 0)) {
        return {
          content: [],
          error: `${label} requires at least ${node.minItems ?? 1} selection${(node.minItems ?? 1) === 1 ? '' : 's'}.`,
          present: false,
        }
      }
      if (!forcePresent) {
        return { content: [], error: '', present: false }
      }
      return { content: [], error: '', present: true }
    }

    if (typeof node.minItems === 'number' && selected.length < node.minItems) {
      return {
        content: [],
        error: `${label} requires at least ${node.minItems} selection${node.minItems === 1 ? '' : 's'}.`,
        present: false,
      }
    }
    if (typeof node.maxItems === 'number' && selected.length > node.maxItems) {
      return {
        content: [],
        error: `${label} allows at most ${node.maxItems} selection${node.maxItems === 1 ? '' : 's'}.`,
        present: false,
      }
    }

    return { content: selected, error: '', present: true }
  }

  if (trimmed.length === 0) {
    if (node.required) {
      return { content: '', error: `${label} is required.`, present: false }
    }
    if (!forcePresent) {
      return { content: '', error: '', present: false }
    }
    if (node.fieldType === 'string' && typeof node.minLength === 'number' && node.minLength > 0) {
      return { content: '', error: `${label} must be at least ${node.minLength} characters.`, present: false }
    }
    if (node.fieldType === 'number' || node.fieldType === 'integer') {
      return { content: '', error: `${label} must be a valid number.`, present: false }
    }
    if (node.fieldType === 'single_select') {
      return { content: '', error: `${label} is required.`, present: false }
    }
    return { content: '', error: '', present: true }
  }

  if (node.fieldType === 'number' || node.fieldType === 'integer') {
    const numericValue = Number(trimmed)
    if (Number.isNaN(numericValue)) {
      return { content: '', error: `${label} must be a valid number.`, present: false }
    }
    if (node.fieldType === 'integer' && !Number.isInteger(numericValue)) {
      return { content: '', error: `${label} must be an integer.`, present: false }
    }
    if (typeof node.minimum === 'number' && numericValue < node.minimum) {
      return { content: '', error: `${label} must be at least ${node.minimum}.`, present: false }
    }
    if (typeof node.maximum === 'number' && numericValue > node.maximum) {
      return { content: '', error: `${label} must be at most ${node.maximum}.`, present: false }
    }
    return {
      content: node.fieldType === 'integer' ? Math.trunc(numericValue) : numericValue,
      error: '',
      present: true,
    }
  }

  if (typeof node.minLength === 'number' && trimmed.length < node.minLength) {
    return {
      content: '',
      error: `${label} must be at least ${node.minLength} characters.`,
      present: false,
    }
  }
  if (typeof node.maxLength === 'number' && trimmed.length > node.maxLength) {
    return {
      content: '',
      error: `${label} must be at most ${node.maxLength} characters.`,
      present: false,
    }
  }
  if (node.fieldType === 'single_select' && !node.options?.some((option) => option.value === trimmed)) {
    return { content: '', error: `${label} must be one of the available options.`, present: false }
  }
	if (node.fieldType === 'string') {
		if (node.format === 'email' && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(trimmed)) {
			return { content: '', error: `${label} must be a valid email address.`, present: false }
		}
    if (node.format === 'uri') {
      try {
        new URL(trimmed)
      } catch {
        return { content: '', error: `${label} must be a valid URL.`, present: false }
      }
    }
		if (node.format === 'date' && !isValidDateString(trimmed)) {
			return { content: '', error: `${label} must be a valid date.`, present: false }
		}
		if (node.format === 'date-time') {
			const normalized = normalizeDateTimeValue(trimmed)
			if (!normalized.valid) {
				return { content: '', error: `${label} must be a valid date and time.`, present: false }
			}
			return { content: normalized.normalized, error: '', present: true }
		}
	}

  return { content: trimmed, error: '', present: true }
}

function buildNodeContent(
  node: ElicitationSchemaNode,
  draft: unknown,
  forcePresent: boolean,
  labelPrefix: string,
): BuildResult {
  if (node.kind === 'primitive') {
    return buildPrimitiveContent(node, draft, forcePresent, labelPrefix || node.label)
  }

	if (node.kind === 'union') {
		const unionDraft =
			isPlainObject(draft) &&
			draft.kind === 'union' &&
      typeof draft.selectedIndex === 'number' &&
      Array.isArray(draft.branches)
        ? (draft as {
            kind: 'union'
            selectedIndex: number
            branches: ElicitationDraftValue[]
          })
        : null

		if (!unionDraft) {
			return {
				content: {},
				error: `${labelPrefix || node.label} uses an invalid branch selection.`,
				present: false,
			}
		}
		if (!forcePresent && !node.required && !isMeaningfulDraftValue(node, unionDraft)) {
			return { content: {}, error: '', present: false }
		}

		const option = node.options[unionDraft.selectedIndex]
    if (!option) {
      return {
        content: {},
        error: `${labelPrefix || node.label} uses an invalid branch selection.`,
        present: false,
      }
    }

    const branchLabel = combineLabel(labelPrefix || node.label, option.label)
    const branchDraft = unionDraft.branches[unionDraft.selectedIndex]
    const branchResult = buildNodeContent(option.node, branchDraft, true, branchLabel)
    if (branchResult.error) {
      return branchResult
    }

    return {
      content: branchResult.content,
      error: '',
      present: true,
    }
  }

  if (node.kind === 'array') {
    if (!forcePresent && !node.required && !isMeaningfulDraftValue(node, draft)) {
      return { content: [], error: '', present: false }
    }

    const items = Array.isArray(draft) ? draft : []
    if (typeof node.minItems === 'number' && items.length < node.minItems) {
      return {
        content: [],
        error: `${labelPrefix || node.label} requires at least ${node.minItems} item${node.minItems === 1 ? '' : 's'}.`,
        present: false,
      }
    }
    if (typeof node.maxItems === 'number' && items.length > node.maxItems) {
      return {
        content: [],
        error: `${labelPrefix || node.label} allows at most ${node.maxItems} item${node.maxItems === 1 ? '' : 's'}.`,
        present: false,
      }
    }

    const content: unknown[] = []
    for (const [index, itemDraft] of items.entries()) {
      const itemLabel = combineLabel(labelPrefix || node.label, `Item ${index + 1}`)
      const itemResult = buildNodeContent(node.item, itemDraft, true, itemLabel)
      if (itemResult.error) {
        return itemResult
      }
      content.push(itemResult.content)
    }

    return {
      content,
      error: '',
      present: true,
    }
  }

  const objectDraft = isPlainObject(draft) ? draft : {}
  if (!forcePresent && !node.required && !isMeaningfulDraftValue(node, objectDraft)) {
    return { content: {}, error: '', present: false }
  }

  const content: Record<string, unknown> = {}
  for (const property of node.properties) {
    const childLabel = combineLabel(labelPrefix, property.label)
    const childResult = buildNodeContent(
      property.node,
      objectDraft[property.name],
      property.required,
      childLabel,
    )
    if (childResult.error) {
      return childResult
    }
    if (childResult.present) {
      content[property.name] = childResult.content
    }
  }

  const propertyCount = Object.keys(content).length
  if (typeof node.minProperties === 'number' && propertyCount < node.minProperties) {
    return {
      content: {},
      error: `${labelPrefix || node.label} requires at least ${node.minProperties} propert${node.minProperties === 1 ? 'y' : 'ies'}.`,
      present: false,
    }
  }
  if (typeof node.maxProperties === 'number' && propertyCount > node.maxProperties) {
    return {
      content: {},
      error: `${labelPrefix || node.label} allows at most ${node.maxProperties} propert${node.maxProperties === 1 ? 'y' : 'ies'}.`,
      present: false,
    }
  }

  return {
    content,
    error: '',
    present: true,
  }
}

export function createInitialElicitationDraftValues(
  node: ElicitationSchemaNode | null | undefined,
): ElicitationDraftValue {
  if (!node) {
    return {}
  }

  if (node.kind === 'primitive') {
    return createInitialDraftFromPrimitive(node)
  }

  if (node.kind === 'union') {
    const branches: ElicitationDraftValue[] = node.options.map((option) =>
      createInitialElicitationDraftValues(option.node),
    )
    return {
      kind: 'union' as const,
      selectedIndex: chooseInitialUnionIndex(node, branches),
      branches,
    }
  }

  if (node.kind === 'array') {
    if (Array.isArray(node.defaultValue)) {
      return node.defaultValue.map((itemDefault) =>
        createInitialElicitationDraftValues({
          ...node.item,
          defaultValue: itemDefault,
        }),
      )
    }
    return []
  }

  const defaultObject = isPlainObject(node.defaultValue) ? node.defaultValue : undefined
  const next: ElicitationDraftObjectValue = {}

  for (const property of node.properties) {
    const childDefault = defaultObject ? defaultObject[property.name] : undefined
    const childNode = property.node
    if (childDefault !== undefined) {
      if (childNode.kind === 'primitive') {
        next[property.name] = createInitialDraftFromPrimitive({
          ...childNode,
          defaultValue: childDefault,
        })
        continue
      }
      if (childNode.kind === 'object' && isPlainObject(childDefault)) {
        next[property.name] = createInitialElicitationDraftValues({
          ...childNode,
          defaultValue: childDefault,
        })
        continue
      }
      if (childNode.kind === 'array' && Array.isArray(childDefault)) {
        next[property.name] = childDefault.map((itemDefault) =>
          createInitialElicitationDraftValues({
            ...childNode.item,
            defaultValue: itemDefault,
          }),
        )
        continue
      }
    }

    next[property.name] = createInitialElicitationDraftValues(property.node)
  }

  return next
}

export function buildElicitationContent(
  node: ElicitationSchemaNode,
  draftValues: ElicitationDraftValue,
) {
  const result = buildNodeContent(node, draftValues, true, node.kind === 'object' ? '' : node.label)
  if (result.error) {
    return {
      content: {},
      error: result.error,
    }
  }

  return {
    content: result.content,
    error: '',
  }
}
