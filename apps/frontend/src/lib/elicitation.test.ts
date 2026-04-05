import { describe, expect, it } from 'vitest'

import {
  buildElicitationContent,
  createInitialElicitationDraftValues,
  normalizeElicitationRequestedSchema,
} from '@/lib/elicitation'

function makeRecursiveSchema() {
  return {
    type: 'object',
    properties: {
      profile: {
        type: 'object',
        title: 'Profile',
        description: 'Primary profile details',
        properties: {
          name: {
            type: 'string',
            title: 'Name',
            default: 'Ada',
            minLength: 2,
            maxLength: 40,
          },
          contact: {
            type: 'object',
            title: 'Contact',
            properties: {
              email: {
                type: 'string',
                title: 'Email',
                format: 'email',
              },
              timezone: {
                type: 'string',
                title: 'Timezone',
                default: 'UTC',
              },
            },
            required: ['email'],
          },
          aliases: {
            type: 'array',
            title: 'Aliases',
            items: {
              type: 'string',
              title: 'Alias',
            },
            default: ['primary', 'work'],
          },
          labels: {
            type: 'array',
            title: 'Labels',
            items: {
              enum: ['alpha', 'beta'],
              enumNames: ['Alpha', 'Beta'],
            },
            default: ['alpha'],
          },
          members: {
            type: 'array',
            title: 'Members',
            minItems: 1,
            items: {
              type: 'object',
              title: 'Member',
              properties: {
                name: {
                  type: 'string',
                  title: 'Member name',
                },
                role: {
                  enum: ['admin', 'viewer'],
                  enumNames: ['Admin', 'Viewer'],
                  default: 'viewer',
                },
              },
              required: ['name'],
            },
          },
        },
        required: ['name', 'contact'],
      },
      delivery: {
        anyOf: [
          {
            title: 'Email',
            type: 'object',
            properties: {
              address: {
                type: 'string',
                title: 'Address',
                format: 'email',
              },
            },
            required: ['address'],
          },
          {
            title: 'Webhook',
            type: 'object',
            properties: {
              endpoint: {
                type: 'string',
                title: 'Endpoint',
                format: 'uri',
              },
            },
            required: ['endpoint'],
          },
        ],
      },
      settings: {
        allOf: [
          {
            type: 'object',
            properties: {
              retries: {
                type: 'integer',
                title: 'Retries',
                minimum: 1,
                maximum: 5,
                default: 2,
              },
            },
          },
          {
            type: 'object',
            properties: {
              note: {
                type: 'string',
                title: 'Note',
                default: 'ship it',
                minLength: 3,
              },
            },
          },
        ],
      },
    },
    required: ['profile', 'delivery', 'settings'],
  }
}

function expectReadyAnalysis(schema: unknown) {
  const analysis = normalizeElicitationRequestedSchema(schema)
  expect(analysis.state).toBe('ready')

  if (analysis.state !== 'ready') {
    throw new Error('expected schema to normalize successfully')
  }

  return analysis.node
}

describe('elicitation helpers', () => {
  it('normalizes recursive schemas and seeds nested defaults', () => {
    const node = expectReadyAnalysis(makeRecursiveSchema())
    const draft = createInitialElicitationDraftValues(node)

    expect(node.kind).toBe('object')
    expect(node.properties).toHaveLength(3)
    expect(node.properties[0]?.name).toBe('profile')
    expect(node.properties[1]?.node.kind).toBe('union')
    expect(node.properties[2]?.node.kind).toBe('object')

    const profileNode = node.properties[0]?.node
    if (profileNode?.kind !== 'object') {
      throw new Error('expected profile to normalize as an object')
    }
    const labelsNode = profileNode.properties.find((property) => property.name === 'labels')?.node
    expect(labelsNode).toMatchObject({
      kind: 'primitive',
      fieldType: 'multi_select',
    })

    const settingsNode = node.properties[2]?.node
    if (settingsNode?.kind !== 'object') {
      throw new Error('expected settings to normalize as an object')
    }
    expect(settingsNode.properties.map((property) => property.name)).toEqual(['retries', 'note'])

    expect(draft).toMatchObject({
      profile: {
        name: 'Ada',
        contact: {
          email: '',
          timezone: 'UTC',
        },
        aliases: ['primary', 'work'],
        labels: ['alpha'],
        members: [],
      },
      delivery: {
        kind: 'union',
        selectedIndex: 0,
        branches: [
          {
            address: '',
          },
          {
            endpoint: '',
          },
        ],
      },
      settings: {
        retries: '2',
        note: 'ship it',
      },
    })
  })

  it('normalizes preserved raw requestedSchema payloads with nested objects and oneOf branches', () => {
    const node = expectReadyAnalysis({
      type: 'object',
      properties: {
        profile: {
          type: 'object',
          properties: {
            name: {
              type: 'string',
              default: 'Ada',
            },
            contact: {
              oneOf: [
                {
                  title: 'Email',
                  type: 'object',
                  properties: {
                    address: {
                      type: 'string',
                      format: 'email',
                    },
                  },
                  required: ['address'],
                },
                {
                  title: 'Webhook',
                  type: 'object',
                  properties: {
                    endpoint: {
                      type: 'string',
                      format: 'uri',
                    },
                  },
                  required: ['endpoint'],
                },
              ],
            },
          },
          required: ['name'],
        },
        tags: {
          type: 'array',
          items: {
            type: 'string',
            enum: ['alpha', 'beta'],
            enumNames: ['Alpha', 'Beta'],
          },
          default: ['alpha'],
        },
      },
      required: ['profile'],
    })

    expect(node.kind).toBe('object')
    if (node.kind !== 'object') {
      throw new Error('expected object root')
    }

    const profileNode = node.properties.find((property) => property.name === 'profile')?.node
    if (profileNode?.kind !== 'object') {
      throw new Error('expected nested profile object')
    }
    expect(profileNode.properties.find((property) => property.name === 'contact')?.node).toMatchObject({
      kind: 'union',
      mode: 'oneOf',
    })

    const tagsNode = node.properties.find((property) => property.name === 'tags')?.node
    expect(tagsNode).toMatchObject({
      kind: 'primitive',
      fieldType: 'multi_select',
    })
  })

  it('keeps open-ended object schemas in manual JSON mode', () => {
    const analysis = normalizeElicitationRequestedSchema({
      type: 'object',
      additionalProperties: {
        type: 'string',
      },
      minProperties: 1,
      properties: {},
    })

    expect(analysis).toMatchObject({
      state: 'unsupported',
      reason: expect.stringMatching(/keyed json content/i),
    })
  })

  it('selects the union branch with the first meaningful default', () => {
    const node = expectReadyAnalysis({
      type: 'object',
      properties: {
        delivery: {
          oneOf: [
            {
              title: 'Email',
              type: 'object',
              properties: {
                address: {
                  type: 'string',
                  title: 'Address',
                },
              },
              required: ['address'],
            },
            {
              title: 'Webhook',
              type: 'object',
              properties: {
                endpoint: {
                  type: 'string',
                  title: 'Endpoint',
                  default: 'https://example.com/hooks',
                  format: 'uri',
                },
              },
              required: ['endpoint'],
            },
          ],
        },
      },
      required: ['delivery'],
    })

    const draft = createInitialElicitationDraftValues(node) as {
      delivery: {
        kind: 'union'
        selectedIndex: number
        branches: Array<Record<string, unknown>>
      }
    }

    expect(draft.delivery.selectedIndex).toBe(1)
    expect(draft.delivery.branches[0]).toEqual({ address: '' })
    expect(draft.delivery.branches[1]).toEqual({ endpoint: 'https://example.com/hooks' })
  })

  it('merges base object properties into composed branches', () => {
    const node = expectReadyAnalysis({
      type: 'object',
      properties: {
        channel: {
          enum: ['email', 'webhook'],
        },
      },
      oneOf: [
        {
          title: 'Email',
          type: 'object',
          properties: {
            address: {
              type: 'string',
              title: 'Address',
              format: 'email',
            },
          },
          required: ['address'],
        },
        {
          title: 'Webhook',
          type: 'object',
          properties: {
            endpoint: {
              type: 'string',
              title: 'Endpoint',
              format: 'uri',
            },
          },
          required: ['endpoint'],
        },
      ],
    })

    expect(node.kind).toBe('union')
    if (node.kind !== 'union') {
      throw new Error('expected composed schema to normalize as a union')
    }

    const firstOption = node.options[0]?.node
    const secondOption = node.options[1]?.node
    if (firstOption?.kind !== 'object' || secondOption?.kind !== 'object') {
      throw new Error('expected composed branches to remain objects')
    }

    expect(firstOption.properties.map((property) => property.name)).toEqual(['channel', 'address'])
    expect(secondOption.properties.map((property) => property.name)).toEqual(['channel', 'endpoint'])

    const draft = createInitialElicitationDraftValues(node) as {
      kind: 'union'
      selectedIndex: number
      branches: Array<Record<string, unknown>>
    }

    expect(draft.selectedIndex).toBe(0)
    expect(draft.branches[0]).toMatchObject({
      channel: '',
      address: '',
    })
    expect(draft.branches[1]).toMatchObject({
      channel: '',
      endpoint: '',
    })

    draft.selectedIndex = 1
    draft.branches[1] = {
      channel: 'webhook',
      endpoint: 'https://example.com/hooks',
    }

    const result = buildElicitationContent(node, draft)
    expect(result.error).toBe('')
    expect(result.content).toEqual({
      channel: 'webhook',
      endpoint: 'https://example.com/hooks',
    })
  })

  it('unwraps a single composed branch without introducing a union', () => {
    const analysis = normalizeElicitationRequestedSchema({
      oneOf: [
        {
          type: 'object',
          title: 'Only branch',
          properties: {
            name: {
              type: 'string',
              title: 'Name',
            },
          },
          required: ['name'],
        },
      ],
    })

    expect(analysis.state).toBe('ready')
    if (analysis.state !== 'ready') {
      throw new Error('expected a single composed branch to normalize successfully')
    }

    expect(analysis.node.kind).toBe('object')
    if (analysis.node.kind !== 'object') {
      throw new Error('expected a single composed branch to unwrap to an object')
    }
    expect(analysis.node.properties.map((property) => property.name)).toEqual(['name'])
  })

  it('merges base object properties into allOf branches', () => {
    const analysis = normalizeElicitationRequestedSchema({
      type: 'object',
      properties: {
        channel: {
          enum: ['email', 'webhook'],
        },
      },
      allOf: [
        {
          type: 'object',
          properties: {
            retries: {
              type: 'integer',
              default: 2,
            },
          },
        },
        {
          type: 'object',
          properties: {
            note: {
              type: 'string',
              default: 'ship it',
            },
          },
        },
      ],
    })

    expect(analysis.state).toBe('ready')
    if (analysis.state !== 'ready') {
      throw new Error('expected allOf base schema to normalize successfully')
    }

    expect(analysis.node.kind).toBe('object')
    if (analysis.node.kind !== 'object') {
      throw new Error('expected allOf merge to normalize as an object')
    }
    expect(analysis.node.properties.map((property) => property.name)).toEqual(['channel', 'retries', 'note'])
  })

  it('preserves validation-only object constraints from allOf branches', () => {
    const analysis = normalizeElicitationRequestedSchema({
      type: 'object',
      properties: {
        channel: {
          enum: ['email', 'webhook'],
        },
        note: {
          type: 'string',
          title: 'Note',
        },
        description: {
          type: 'string',
          title: 'Description',
        },
      },
      allOf: [
        {
          required: ['channel'],
        },
        {
          minProperties: 2,
        },
      ],
    })

    expect(analysis.state).toBe('ready')
    if (analysis.state !== 'ready') {
      throw new Error('expected allOf validation overlays to normalize successfully')
    }

    expect(analysis.node.kind).toBe('object')
    if (analysis.node.kind !== 'object') {
      throw new Error('expected allOf validation overlays to normalize as an object')
    }

    expect(analysis.node).toMatchObject({
      minProperties: 2,
    })

    const channelProperty = analysis.node.properties.find((property) => property.name === 'channel')
    expect(channelProperty?.required).toBe(true)

    const draft = createInitialElicitationDraftValues(analysis.node) as {
      channel: string
      note: string
      description: string
    }
    draft.channel = 'email'

    const invalid = buildElicitationContent(analysis.node, draft)
    expect(invalid.error).toMatch(/at least 2 properties/i)

    draft.note = 'ship it'

    const valid = buildElicitationContent(analysis.node, draft)
    expect(valid).toEqual({
      content: {
        channel: 'email',
        note: 'ship it',
      },
      error: '',
    })
  })

  it('preserves validation-only object constraints from composed schema bases', () => {
    const analysis = normalizeElicitationRequestedSchema({
      required: ['kind'],
      oneOf: [
        {
          title: 'Email',
          type: 'object',
          properties: {
            kind: {
              enum: ['email'],
            },
            address: {
              type: 'string',
              title: 'Address',
            },
          },
          required: ['address'],
        },
        {
          title: 'Webhook',
          type: 'object',
          properties: {
            kind: {
              enum: ['webhook'],
            },
            endpoint: {
              type: 'string',
              title: 'Endpoint',
            },
          },
          required: ['endpoint'],
        },
      ],
    })

    expect(analysis.state).toBe('ready')
    if (analysis.state !== 'ready') {
      throw new Error('expected composed schema validation overlays to normalize successfully')
    }

    expect(analysis.node.kind).toBe('union')
    if (analysis.node.kind !== 'union') {
      throw new Error('expected composed schema validation overlays to normalize as a union')
    }

    const firstOption = analysis.node.options[0]?.node
    if (firstOption?.kind !== 'object') {
      throw new Error('expected the first composed option to normalize as an object')
    }
    expect(firstOption.properties.find((property) => property.name === 'kind')?.required).toBe(true)

    const draft = createInitialElicitationDraftValues(analysis.node) as {
      kind: 'union'
      selectedIndex: number
      branches: Array<
        | {
            kind?: string
            address?: string
          }
        | {
            kind?: string
            endpoint?: string
          }
      >
    }

    draft.branches[0] = {
      address: 'ops@example.com',
    }

    const invalid = buildElicitationContent(analysis.node, draft)
    expect(invalid.error).toMatch(/kind is required/i)

    draft.branches[0] = {
      kind: 'email',
      address: 'ops@example.com',
    }

    const valid = buildElicitationContent(analysis.node, draft)
    expect(valid).toEqual({
      content: {
        address: 'ops@example.com',
        kind: 'email',
      },
      error: '',
    })
  })

  it('merges single-select allOf branches into a single choice field', () => {
    const node = expectReadyAnalysis({
      allOf: [
        {
          type: 'string',
          title: 'Channel',
          enum: ['email', 'webhook'],
          default: 'webhook',
        },
        {
          type: 'string',
          enum: ['webhook', 'sms'],
        },
      ],
    })

    expect(node).toMatchObject({
      kind: 'primitive',
      fieldType: 'single_select',
      defaultValue: 'webhook',
    })
    if (node.kind !== 'primitive' || node.fieldType !== 'single_select') {
      throw new Error('expected the merged schema to normalize as a single-select field')
    }

    expect(node.options).toEqual([
      {
        value: 'webhook',
        label: 'webhook',
      },
    ])

    const draft = createInitialElicitationDraftValues(node)
    expect(draft).toBe('webhook')

    const result = buildElicitationContent(node, draft)
    expect(result).toEqual({
      content: 'webhook',
      error: '',
    })
  })

  it('merges multi-select allOf branches into a constrained multi-select field', () => {
    const node = expectReadyAnalysis({
      allOf: [
        {
          type: 'array',
          title: 'Labels',
          minItems: 1,
          maxItems: 3,
          items: {
            enum: ['alpha', 'beta', 'gamma'],
            enumNames: ['Alpha', 'Beta', 'Gamma'],
          },
        },
        {
          type: 'array',
          minItems: 2,
          maxItems: 2,
          items: {
            enum: ['beta', 'gamma', 'delta'],
          },
        },
      ],
    })

    expect(node).toMatchObject({
      kind: 'primitive',
      fieldType: 'multi_select',
      minItems: 2,
      maxItems: 2,
    })
    if (node.kind !== 'primitive' || node.fieldType !== 'multi_select') {
      throw new Error('expected the merged schema to normalize as a multi-select field')
    }

    expect(node.options).toEqual([
      {
        value: 'beta',
        label: 'Beta',
      },
      {
        value: 'gamma',
        label: 'Gamma',
      },
    ])

    const draft = createInitialElicitationDraftValues(node)
    expect(draft).toEqual([])
    const result = buildElicitationContent(node, ['beta', 'gamma'])
    expect(result).toEqual({
      content: ['beta', 'gamma'],
      error: '',
    })
  })

  it('merges numeric allOf branches into an integer field', () => {
    const node = expectReadyAnalysis({
      allOf: [
        {
          type: 'integer',
          title: 'Retries',
          minimum: 1,
        },
        {
          type: 'number',
          maximum: 5,
        },
      ],
    })

    expect(node).toMatchObject({
      kind: 'primitive',
      fieldType: 'integer',
      minimum: 1,
      maximum: 5,
    })

    const result = buildElicitationContent(node, '4')
    expect(result).toEqual({
      content: 4,
      error: '',
    })
  })

  it('merges union allOf branches by aligning branch options', () => {
    const node = expectReadyAnalysis({
      allOf: [
        {
          oneOf: [
            {
              title: 'Short',
              type: 'string',
              minLength: 2,
            },
            {
              title: 'Long',
              type: 'string',
              minLength: 4,
            },
          ],
        },
        {
          oneOf: [
            {
              title: 'Short',
              type: 'string',
              maxLength: 3,
            },
            {
              title: 'Long',
              type: 'string',
              maxLength: 6,
            },
          ],
        },
      ],
    })

    expect(node.kind).toBe('union')
    if (node.kind !== 'union') {
      throw new Error('expected the merged schema to normalize as a union')
    }

    expect(node.options).toHaveLength(2)
    expect(node.options[0]?.node).toMatchObject({
      kind: 'primitive',
      fieldType: 'string',
      minLength: 2,
      maxLength: 3,
    })
    expect(node.options[1]?.node).toMatchObject({
      kind: 'primitive',
      fieldType: 'string',
      minLength: 4,
      maxLength: 6,
    })

    const draft = createInitialElicitationDraftValues(node) as {
      kind: 'union'
      selectedIndex: number
      branches: string[]
    }
    draft.selectedIndex = 0
    draft.branches[0] = 'ab'

    const result = buildElicitationContent(node, draft)
    expect(result).toEqual({
      content: 'ab',
      error: '',
    })
  })

  it('seeds primitive defaults and ignores invalid enum defaults', () => {
    const node = expectReadyAnalysis({
      type: 'object',
      properties: {
        enabled: {
          type: 'boolean',
          title: 'Enabled',
          default: false,
        },
        mode: {
          enum: ['one', 'two'],
          default: 'missing',
        },
        labels: {
          type: 'array',
          title: 'Labels',
          items: {
            enum: ['alpha', 'beta'],
          },
          default: ['alpha', 'missing'],
        },
        count: {
          type: 'integer',
          title: 'Count',
          default: 0,
        },
      },
      required: ['enabled', 'mode', 'count'],
    })

    const draft = createInitialElicitationDraftValues(node) as {
      enabled: boolean
      mode: string
      labels: string[]
      count: string
    }

    expect(draft).toEqual({
      enabled: false,
      mode: '',
      labels: ['alpha'],
      count: '0',
    })
  })

  it('builds recursive payloads from nested drafts', () => {
    const node = expectReadyAnalysis(makeRecursiveSchema())
    const draft = createInitialElicitationDraftValues(node) as {
      profile: {
        name: string
        contact: {
          email: string
          timezone: string
        }
        aliases: string[]
        labels: string[]
        members: Array<{
          name: string
          role: string
        }>
      }
      delivery: {
        kind: 'union'
        selectedIndex: number
        branches: Array<
          | {
              address?: string
            }
          | {
              endpoint?: string
            }
        >
      }
      settings: {
        retries: string
        note: string
      }
    }

    draft.profile.name = 'Ada Lovelace'
    draft.profile.contact.email = 'ada@example.com'
    draft.profile.aliases = ['primary', 'secondary']
    draft.profile.labels = ['alpha', 'beta']
    draft.profile.members = [
      {
        name: 'Grace',
        role: 'admin',
      },
    ]
    draft.delivery.selectedIndex = 1
    draft.delivery.branches[1] = {
      endpoint: 'https://example.com/hooks',
    }
    draft.settings.retries = '4'
    draft.settings.note = 'Ship it'

    const result = buildElicitationContent(node, draft)

    expect(result.error).toBe('')
    expect(result.content).toMatchObject({
      profile: {
        name: 'Ada Lovelace',
        contact: {
          email: 'ada@example.com',
          timezone: 'UTC',
        },
        aliases: ['primary', 'secondary'],
        labels: ['alpha', 'beta'],
        members: [
          {
            name: 'Grace',
            role: 'admin',
          },
        ],
      },
      delivery: {
        endpoint: 'https://example.com/hooks',
      },
      settings: {
        retries: 4,
        note: 'Ship it',
      },
    })
  })

  it.each([
    ['email', 'ops@example.com', 'ops@example.com'],
    ['uri', 'https://example.com/path', 'https://example.com/path'],
    ['date', '2026-03-29', '2026-03-29'],
    ['date-time', '2026-03-29T10:15:00Z', '2026-03-29T10:15:00Z'],
  ] as const)('validates nested %s strings', (format, value, expected) => {
    const node = expectReadyAnalysis({
      type: 'object',
      properties: {
        details: {
          type: 'object',
          properties: {
            value: {
              type: 'string',
              title: 'Value',
              format,
            },
          },
          required: ['value'],
        },
      },
      required: ['details'],
    })

    const draft = createInitialElicitationDraftValues(node) as {
      details: {
        value: string
      }
    }
    draft.details.value = value

    const result = buildElicitationContent(node, draft)

    expect(result.error).toBe('')
    expect(result.content).toEqual({
      details: {
        value: expected,
      },
    })
  })

  it('rejects invalid date strings', () => {
    const node = expectReadyAnalysis({
      type: 'object',
      properties: {
        details: {
          type: 'object',
          properties: {
            value: {
              type: 'string',
              title: 'Value',
              format: 'date',
            },
          },
          required: ['value'],
        },
      },
      required: ['details'],
    })

    const draft = createInitialElicitationDraftValues(node) as {
      details: {
        value: string
      }
    }
    draft.details.value = '2026-02-31'

    const result = buildElicitationContent(node, draft)

    expect(result.error).toBe('Details / Value must be a valid date.')
  })

  it('preserves union branch drafts when switching selections', () => {
    const node = expectReadyAnalysis({
      type: 'object',
      properties: {
        delivery: {
          oneOf: [
            {
              title: 'Email',
              type: 'object',
              properties: {
                address: {
                  type: 'string',
                  title: 'Address',
                  format: 'email',
                },
              },
              required: ['address'],
            },
            {
              title: 'Webhook',
              type: 'object',
              properties: {
                endpoint: {
                  type: 'string',
                  title: 'Endpoint',
                  format: 'uri',
                },
              },
              required: ['endpoint'],
            },
          ],
        },
      },
      required: ['delivery'],
    })

    const draft = createInitialElicitationDraftValues(node) as {
      delivery: {
        kind: 'union'
        selectedIndex: number
        branches: Array<
          | {
              address?: string
            }
          | {
              endpoint?: string
            }
        >
      }
    }

    draft.delivery.branches[0] = {
      address: 'ops@example.com',
    }
    draft.delivery.selectedIndex = 1
    draft.delivery.branches[1] = {
      endpoint: 'https://example.com/hooks',
    }
    draft.delivery.selectedIndex = 0

    const result = buildElicitationContent(node, draft)

    expect(result.error).toBe('')
    expect(draft.delivery.branches[0]).toEqual({
      address: 'ops@example.com',
    })
    expect(draft.delivery.branches[1]).toEqual({
      endpoint: 'https://example.com/hooks',
    })
    expect(result.content).toEqual({
      delivery: {
        address: 'ops@example.com',
      },
    })
  })

  it('omits optional blank fields from the built payload', () => {
    const node = expectReadyAnalysis({
      type: 'object',
      properties: {
        required_name: {
          type: 'string',
          title: 'Required name',
        },
        optional_note: {
          type: 'string',
          title: 'Optional note',
        },
        optional_group: {
          type: 'object',
          title: 'Optional group',
          properties: {
            description: {
              type: 'string',
              title: 'Description',
            },
          },
        },
        optional_items: {
          type: 'array',
          title: 'Optional items',
          items: {
            type: 'string',
            title: 'Item',
          },
        },
      },
      required: ['required_name'],
    })

    const draft = createInitialElicitationDraftValues(node) as {
      required_name: string
      optional_note: string
      optional_group: {
        description: string
      }
      optional_items: string[]
    }

    draft.required_name = 'Ada'
    draft.optional_note = ''
    draft.optional_group.description = ''
    draft.optional_items = []

    const result = buildElicitationContent(node, draft)

    expect(result.error).toBe('')
    expect(result.content).toEqual({
      required_name: 'Ada',
    })
  })

  it('omits untouched optional booleans from the built payload', () => {
    const node = expectReadyAnalysis({
      type: 'object',
      properties: {
        required_name: {
          type: 'string',
          title: 'Required name',
        },
        notify: {
          type: 'boolean',
          title: 'Notify',
        },
      },
      required: ['required_name'],
    })

    const draft = createInitialElicitationDraftValues(node) as {
      required_name: string
      notify: string | boolean
    }

    draft.required_name = 'Ada'

    const result = buildElicitationContent(node, draft)

    expect(result.error).toBe('')
    expect(result.content).toEqual({
      required_name: 'Ada',
    })

    draft.notify = false

    const explicitFalse = buildElicitationContent(node, draft)

    expect(explicitFalse.error).toBe('')
    expect(explicitFalse.content).toEqual({
      required_name: 'Ada',
      notify: false,
    })
  })

  it('omits untouched optional unions from the built payload', () => {
    const node = expectReadyAnalysis({
      type: 'object',
      properties: {
        required_name: {
          type: 'string',
          title: 'Required name',
        },
        delivery: {
          oneOf: [
            {
              title: 'Email',
              type: 'object',
              properties: {
                address: {
                  type: 'string',
                  title: 'Address',
                  format: 'email',
                },
              },
              required: ['address'],
            },
            {
              title: 'Webhook',
              type: 'object',
              properties: {
                endpoint: {
                  type: 'string',
                  title: 'Endpoint',
                  format: 'uri',
                },
              },
              required: ['endpoint'],
            },
          ],
        },
      },
      required: ['required_name'],
    })

    const draft = createInitialElicitationDraftValues(node) as {
      required_name: string
      delivery: {
        kind: 'union'
        selectedIndex: number
        branches: Array<Record<string, unknown>>
      }
    }

    draft.required_name = 'Ada'

    const result = buildElicitationContent(node, draft)

    expect(result.error).toBe('')
    expect(result.content).toEqual({
      required_name: 'Ada',
    })
  })

  it('normalizes local date-time values to RFC3339 output', () => {
    const node = expectReadyAnalysis({
      type: 'object',
      properties: {
        scheduled_at: {
          type: 'string',
          title: 'Scheduled at',
          format: 'date-time',
        },
      },
      required: ['scheduled_at'],
    })

    const draft = createInitialElicitationDraftValues(node) as {
      scheduled_at: string
    }
    draft.scheduled_at = '2026-03-29T10:15'

    const result = buildElicitationContent(node, draft)

    expect(result.error).toBe('')
    expect(result.content).toEqual({
      scheduled_at: new Date('2026-03-29T10:15').toISOString(),
    })
  })

  it.each([
    {
      label: 'missing root',
      schema: null,
      reason: /missing or malformed/i,
    },
    {
      label: 'plain empty schema',
      schema: {},
      reason: /unsupported schema shape/i,
    },
    {
      label: 'schema references',
      schema: {
        $ref: '#/definitions/Contact',
      },
      reason: /unsupported schema references/i,
    },
    {
      label: 'invalid required list type',
      schema: {
        type: 'object',
        properties: {
          name: {
            type: 'string',
          },
        },
        required: 'name',
      },
      reason: /invalid required list/i,
    },
    {
      label: 'invalid required list item',
      schema: {
        type: 'object',
        properties: {
          name: {
            type: 'string',
          },
        },
        required: [1],
      },
      reason: /invalid required list/i,
    },
    {
      label: 'mixes object and array shapes',
      schema: {
        type: 'object',
        properties: {
          name: {
            type: 'string',
          },
        },
        items: {
          type: 'string',
        },
      },
      reason: /mixes object and array schema shapes/i,
    },
    {
      label: 'mixes object and enum shapes',
      schema: {
        type: 'object',
        properties: {
          name: {
            type: 'string',
          },
        },
        enum: ['alpha'],
      },
      reason: /mixes object and enum schema shapes/i,
    },
    {
      label: 'unsupported object schema shape',
      schema: {
        type: 'object',
        properties: 'not-an-object',
      },
      reason: /unsupported object schema shape/i,
    },
    {
      label: 'unsupported string format',
      schema: {
        type: 'string',
        format: 'uuid',
      },
      reason: /unsupported string format/i,
    },
    {
      label: 'invalid string length',
      schema: {
        type: 'string',
        minLength: -1,
      },
      reason: /invalid string length constraints/i,
    },
    {
      label: 'invalid numeric constraint',
      schema: {
        type: 'integer',
        minimum: 'not-a-number',
      },
      reason: /invalid numeric constraints/i,
    },
    {
      label: 'unsupported array shape',
      schema: {
        type: 'array',
      },
      reason: /unsupported array schema shape/i,
    },
    {
      label: 'invalid array constraints',
      schema: {
        type: 'array',
        minItems: -1,
        items: {
          type: 'string',
        },
      },
      reason: /invalid array constraints/i,
    },
    {
      label: 'unsupported array item shape',
      schema: {
        type: 'array',
        items: 1,
      },
      reason: /unsupported array item shape/i,
    },
    {
      label: 'unsupported array item select-like shape',
      schema: {
        type: 'array',
        items: {
          type: 'number',
          enum: ['one'],
        },
      },
      reason: /unsupported array item shape/i,
    },
    {
      label: 'empty enum options',
      schema: {
        type: 'string',
        enum: [],
      },
      reason: /does not define any enum options/i,
    },
    {
      label: 'invalid enum values',
      schema: {
        type: 'string',
        enum: [1, 2],
      },
      reason: /unsupported enum value/i,
    },
    {
      label: 'unsupported enum shape',
      schema: {
        type: 'number',
        enum: ['one'],
      },
      reason: /unsupported enum shape/i,
    },
    {
      label: 'invalid enum option shape',
      schema: {
        oneOf: [
          {
            const: 1,
            title: 'One',
          },
        ],
      },
      reason: /unsupported enum option shape/i,
    },
    {
      label: 'incompatible composed branches',
      schema: {
        oneOf: [
          {
            type: 'string',
          },
        ],
        anyOf: [
          {
            type: 'string',
          },
        ],
      },
      reason: /incompatible composed schema branches/i,
    },
    {
      label: 'empty composed branches',
      schema: {
        oneOf: [],
      },
      reason: /unsupported composed schema shape/i,
    },
    {
      label: 'empty allOf branches',
      schema: {
        allOf: [],
      },
      reason: /unsupported allOf schema shape/i,
    },
    {
      label: 'unsupported composed branch shape',
      schema: {
        oneOf: [null],
      },
      reason: /unsupported composed branch shape/i,
    },
    {
      label: 'tuple arrays',
      schema: {
        type: 'array',
        items: [
          {
            type: 'string',
          },
        ],
      },
      reason: /tuple arrays/,
    },
    {
      label: 'unknown required fields',
      schema: {
        type: 'object',
        properties: {
          name: {
            type: 'string',
          },
        },
        required: ['missing'],
      },
      reason: /unknown field/i,
    },
    {
      label: 'conflicting allOf branches',
      schema: {
        allOf: [
          {
            type: 'object',
            properties: {
              limit: {
                type: 'integer',
                minimum: 10,
              },
            },
          },
          {
            type: 'object',
            properties: {
              limit: {
                type: 'integer',
                maximum: 5,
              },
            },
          },
        ],
      },
      reason: /conflicting numeric constraints/i,
    },
    {
      label: 'malformed oneOf branches',
      schema: {
        oneOf: [
          {
            title: 'Ok',
            type: 'object',
            properties: {},
          },
          'nope',
        ],
      },
      reason: /unsupported composed branch shape/i,
    },
  ])('rejects malformed or conflicting shapes: $label', ({ schema, reason }) => {
    const analysis = normalizeElicitationRequestedSchema(schema)

    expect(analysis.state).toBe('unsupported')
    if (analysis.state === 'unsupported') {
      expect(analysis.reason).toMatch(reason)
    }
  })

  it.each([
    {
      label: 'conflicting string formats',
      schema: {
        allOf: [
          {
            type: 'string',
            format: 'email',
          },
          {
            type: 'string',
            format: 'uri',
          },
        ],
      },
      reason: /conflicting string formats/i,
    },
    {
      label: 'conflicting enum options',
      schema: {
        allOf: [
          {
            type: 'string',
            enum: ['alpha'],
          },
          {
            type: 'string',
            enum: ['beta'],
          },
        ],
      },
      reason: /conflicting enum options/i,
    },
    {
      label: 'conflicting multi-select constraints',
      schema: {
        allOf: [
          {
            type: 'array',
            minItems: 2,
            items: {
              enum: ['alpha', 'beta'],
            },
          },
          {
            type: 'array',
            maxItems: 1,
            items: {
              enum: ['alpha', 'beta'],
            },
          },
        ],
      },
      reason: /conflicting multi-select constraints/i,
    },
    {
      label: 'conflicting string length constraints',
      schema: {
        allOf: [
          {
            type: 'string',
            minLength: 3,
          },
          {
            type: 'string',
            maxLength: 2,
          },
        ],
      },
      reason: /conflicting string length constraints/i,
    },
    {
      label: 'conflicting primitive shapes',
      schema: {
        allOf: [
          {
            type: 'string',
          },
          {
            type: 'integer',
          },
        ],
      },
      reason: /conflicting primitive shapes/i,
    },
    {
      label: 'conflicting schema shapes',
      schema: {
        allOf: [
          {
            type: 'object',
            properties: {
              name: {
                type: 'string',
              },
            },
          },
          {
            type: 'array',
            items: {
              type: 'string',
            },
          },
        ],
      },
      reason: /conflicting schema shapes/i,
    },
    {
      label: 'conflicting array constraints',
      schema: {
        allOf: [
          {
            type: 'array',
            items: {
              type: 'string',
            },
            minItems: 3,
          },
          {
            type: 'array',
            items: {
              type: 'string',
            },
            maxItems: 2,
          },
        ],
      },
      reason: /conflicting array constraints/i,
    },
    {
      label: 'conflicting boolean defaults',
      schema: {
        allOf: [
          {
            type: 'object',
            properties: {
              enabled: {
                type: 'boolean',
                default: true,
              },
            },
          },
          {
            type: 'object',
            properties: {
              enabled: {
                type: 'boolean',
                default: false,
              },
            },
          },
        ],
      },
      reason: /conflicting defaults/i,
    },
    {
      label: 'conflicting composed branches',
      schema: {
        allOf: [
          {
            oneOf: [
              {
                type: 'string',
              },
              {
                type: 'string',
              },
            ],
          },
          {
            oneOf: [
              {
                type: 'string',
              },
              {
                type: 'string',
              },
              {
                type: 'string',
              },
            ],
          },
        ],
      },
      reason: /conflicting composed branches/i,
    },
  ])('rejects conflicting allOf combinations: $label', ({ schema, reason }) => {
    const analysis = normalizeElicitationRequestedSchema(schema)

    expect(analysis.state).toBe('unsupported')
    if (analysis.state === 'unsupported') {
      expect(analysis.reason).toMatch(reason)
    }
  })

  it.each([
    {
      label: 'required string',
      schema: {
        type: 'object',
        properties: {
          name: {
            type: 'string',
            title: 'Name',
          },
        },
        required: ['name'],
      },
      mutate: (draft: Record<string, unknown>) => {
        draft.name = ''
      },
      reason: /Name is required\./,
    },
    {
      label: 'string length',
      schema: {
        type: 'object',
        properties: {
          name: {
            type: 'string',
            title: 'Name',
            minLength: 3,
            maxLength: 5,
          },
        },
        required: ['name'],
      },
      mutate: (draft: Record<string, unknown>) => {
        draft.name = 'Al'
      },
      reason: /must be at least 3 characters/i,
    },
    {
      label: 'string too long',
      schema: {
        type: 'object',
        properties: {
          name: {
            type: 'string',
            title: 'Name',
            minLength: 3,
            maxLength: 5,
          },
        },
        required: ['name'],
      },
      mutate: (draft: Record<string, unknown>) => {
        draft.name = 'Alphabet'
      },
      reason: /must be at most 5 characters/i,
    },
    {
      label: 'invalid email',
      schema: {
        type: 'object',
        properties: {
          email: {
            type: 'string',
            title: 'Email',
            format: 'email',
          },
        },
        required: ['email'],
      },
      mutate: (draft: Record<string, unknown>) => {
        draft.email = 'not-an-email'
      },
      reason: /must be a valid email address/i,
    },
    {
      label: 'invalid uri',
      schema: {
        type: 'object',
        properties: {
          endpoint: {
            type: 'string',
            title: 'Endpoint',
            format: 'uri',
          },
        },
        required: ['endpoint'],
      },
      mutate: (draft: Record<string, unknown>) => {
        draft.endpoint = 'not-a-url'
      },
      reason: /must be a valid URL/i,
    },
    {
      label: 'invalid number value',
      schema: {
        type: 'object',
        properties: {
          retries: {
            type: 'number',
            title: 'Retries',
          },
        },
        required: ['retries'],
      },
      mutate: (draft: Record<string, unknown>) => {
        draft.retries = 'abc'
      },
      reason: /must be a valid number/i,
    },
    {
      label: 'invalid date-time',
      schema: {
        type: 'object',
        properties: {
          scheduled_at: {
            type: 'string',
            title: 'Scheduled at',
            format: 'date-time',
          },
        },
        required: ['scheduled_at'],
      },
      mutate: (draft: Record<string, unknown>) => {
        draft.scheduled_at = 'not-a-date-time'
      },
      reason: /must be a valid date and time/i,
    },
    {
      label: 'invalid integer',
      schema: {
        type: 'object',
        properties: {
          retries: {
            type: 'integer',
            title: 'Retries',
            minimum: 1,
            maximum: 5,
          },
        },
        required: ['retries'],
      },
      mutate: (draft: Record<string, unknown>) => {
        draft.retries = '1.5'
      },
      reason: /must be an integer/i,
    },
    {
      label: 'invalid number range',
      schema: {
        type: 'object',
        properties: {
          ratio: {
            type: 'number',
            title: 'Ratio',
            minimum: 0.5,
            maximum: 1.5,
          },
        },
        required: ['ratio'],
      },
      mutate: (draft: Record<string, unknown>) => {
        draft.ratio = '0.25'
      },
      reason: /must be at least 0.5/i,
    },
    {
      label: 'number maximum',
      schema: {
        type: 'object',
        properties: {
          ratio: {
            type: 'number',
            title: 'Ratio',
            minimum: 0.5,
            maximum: 1.5,
          },
        },
        required: ['ratio'],
      },
      mutate: (draft: Record<string, unknown>) => {
        draft.ratio = '1.75'
      },
      reason: /must be at most 1.5/i,
    },
    {
      label: 'invalid single select option',
      schema: {
        type: 'object',
        properties: {
          mode: {
            type: 'string',
            title: 'Mode',
            enum: ['auto', 'manual'],
          },
        },
        required: ['mode'],
      },
      mutate: (draft: Record<string, unknown>) => {
        draft.mode = 'unexpected'
      },
      reason: /must be one of the available options/i,
    },
    {
      label: 'multi-select minimum',
      schema: {
        type: 'object',
        properties: {
          labels: {
            type: 'array',
            title: 'Labels',
            items: {
              enum: ['alpha', 'beta', 'gamma'],
            },
            minItems: 2,
          },
        },
        required: ['labels'],
      },
      mutate: (draft: Record<string, unknown>) => {
        draft.labels = ['alpha']
      },
      reason: /requires at least 2 selections/i,
    },
    {
      label: 'multi-select empty',
      schema: {
        type: 'object',
        properties: {
          labels: {
            type: 'array',
            title: 'Labels',
            items: {
              enum: ['alpha', 'beta', 'gamma'],
            },
            minItems: 2,
          },
        },
        required: ['labels'],
      },
      mutate: (draft: Record<string, unknown>) => {
        draft.labels = []
      },
      reason: /requires at least 2 selections/i,
    },
    {
      label: 'multi-select maximum',
      schema: {
        type: 'object',
        properties: {
          labels: {
            type: 'array',
            title: 'Labels',
            items: {
              enum: ['alpha', 'beta', 'gamma'],
            },
            maxItems: 2,
          },
        },
        required: ['labels'],
      },
      mutate: (draft: Record<string, unknown>) => {
        draft.labels = ['alpha', 'beta', 'gamma']
      },
      reason: /allows at most 2 selections/i,
    },
    {
      label: 'array minimum',
      schema: {
        type: 'object',
        properties: {
          recipients: {
            type: 'array',
            title: 'Recipients',
            minItems: 2,
            items: {
              type: 'object',
              properties: {
                name: {
                  type: 'string',
                },
              },
              required: ['name'],
            },
          },
        },
        required: ['recipients'],
      },
      mutate: (draft: Record<string, unknown>) => {
        draft.recipients = [{ name: 'Ada' }]
      },
      reason: /requires at least 2 items/i,
    },
    {
      label: 'array maximum',
      schema: {
        type: 'object',
        properties: {
          recipients: {
            type: 'array',
            title: 'Recipients',
            maxItems: 2,
            items: {
              type: 'object',
              properties: {
                name: {
                  type: 'string',
                },
              },
              required: ['name'],
            },
          },
        },
        required: ['recipients'],
      },
      mutate: (draft: Record<string, unknown>) => {
        draft.recipients = [{ name: 'Ada' }, { name: 'Grace' }, { name: 'Linus' }]
      },
      reason: /allows at most 2 items/i,
    },
    {
      label: 'invalid union selection',
      schema: {
        type: 'object',
        properties: {
          delivery: {
            oneOf: [
              {
                title: 'Email',
                type: 'object',
                properties: {
                  address: {
                    type: 'string',
                  },
                },
                required: ['address'],
              },
              {
                title: 'Webhook',
                type: 'object',
                properties: {
                  endpoint: {
                    type: 'string',
                  },
                },
                required: ['endpoint'],
              },
            ],
          },
        },
        required: ['delivery'],
      },
      mutate: (draft: Record<string, unknown>) => {
        const delivery = draft.delivery as {
          kind: 'union'
          selectedIndex: number
          branches: Array<Record<string, unknown>>
        }
        delivery.selectedIndex = 5
      },
      reason: /invalid branch selection/i,
    },
  ])('rejects invalid payloads: $label', ({ schema, mutate, reason }) => {
    const node = expectReadyAnalysis(schema)
    const draft = createInitialElicitationDraftValues(node)
    mutate(draft as Record<string, unknown>)

    const result = buildElicitationContent(node, draft)

    expect(result.error).toMatch(reason)
    expect(result.content).toEqual({})
  })

  it.each([
    {
      label: 'blank primitive string branch',
      schema: {
        type: 'object',
        properties: {
          value: {
            oneOf: [
              {
                title: 'Code',
                type: 'string',
                minLength: 2,
              },
              {
                title: 'Count',
                type: 'integer',
                minimum: 1,
              },
            ],
          },
        },
        required: ['value'],
      },
      mutate: (draft: Record<string, unknown>) => {
        const value = draft.value as {
          kind: 'union'
          selectedIndex: number
          branches: string[]
        }
        value.selectedIndex = 0
        value.branches[0] = ''
      },
      reason: /must be at least 2 characters/i,
    },
    {
      label: 'blank primitive number branch',
      schema: {
        type: 'object',
        properties: {
          value: {
            oneOf: [
              {
                title: 'Code',
                type: 'string',
                minLength: 2,
              },
              {
                title: 'Count',
                type: 'integer',
                minimum: 1,
              },
            ],
          },
        },
        required: ['value'],
      },
      mutate: (draft: Record<string, unknown>) => {
        const value = draft.value as {
          kind: 'union'
          selectedIndex: number
          branches: string[]
        }
        value.selectedIndex = 1
        value.branches[1] = ''
      },
      reason: /must be a valid number/i,
    },
    {
      label: 'blank primitive select branch',
      schema: {
        type: 'object',
        properties: {
          choice: {
            oneOf: [
              {
                title: 'One',
                enum: ['one', 'two'],
              },
              {
                title: 'Other',
                type: 'object',
                properties: {
                  name: {
                    type: 'string',
                  },
                },
              },
            ],
          },
        },
        required: ['choice'],
      },
      mutate: (draft: Record<string, unknown>) => {
        const choice = draft.choice as {
          kind: 'union'
          selectedIndex: number
          branches: Array<Record<string, unknown> | string>
        }
        choice.selectedIndex = 0
        choice.branches[0] = ''
      },
      reason: /is required\./i,
    },
  ])('rejects blank primitive union branches: $label', ({ schema, mutate, reason }) => {
    const node = expectReadyAnalysis(schema)
    const draft = createInitialElicitationDraftValues(node)
    mutate(draft as Record<string, unknown>)

    const result = buildElicitationContent(node, draft)

    expect(result.error).toMatch(reason)
    expect(result.content).toEqual({})
  })

  it('returns empty for an object with no fields', () => {
    const analysis = normalizeElicitationRequestedSchema({
      type: 'object',
      properties: {},
    })

    expect(analysis).toEqual({
      state: 'empty',
      node: null,
    })
  })
})
