import { fireEvent, render, screen } from '@testing-library/react'
import { vi } from 'vitest'

import { ElicitationForm } from '@/components/dashboard/elicitation-form'
import { selectOption } from '@/test/test-utils'

function renderElicitationForm(overrides: {
  draftKey?: string
  requestedSchema?: unknown
  disabled?: boolean
  isSubmitting?: boolean
  onAccept?: (content: unknown) => void
  onDecline?: () => void
  onCancel?: () => void
} = {}) {
  const onAccept = overrides.onAccept ?? vi.fn()
  const onDecline = overrides.onDecline ?? vi.fn()
  const onCancel = overrides.onCancel ?? vi.fn()

  return render(
    <ElicitationForm
      draftKey={overrides.draftKey ?? 'test-draft-key'}
      disabled={overrides.disabled ?? false}
      isSubmitting={overrides.isSubmitting ?? false}
      requestedSchema={overrides.requestedSchema ?? makeRecursiveFormSchema()}
      onAccept={onAccept}
      onCancel={onCancel}
      onDecline={onDecline}
    />,
  )
}

function makeRecursiveFormSchema() {
  return {
    type: 'object',
    properties: {
      profile: {
        type: 'object',
        title: 'Profile',
        properties: {
          name: {
            type: 'string',
            title: 'Name',
            default: 'Ada',
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
            },
            required: ['email'],
          },
          active: {
            type: 'boolean',
            title: 'Active',
            default: true,
          },
          role: {
            type: 'string',
            title: 'Role',
            enum: ['engineer', 'manager'],
            enumNames: ['Engineer', 'Manager'],
            default: 'engineer',
          },
          tags: {
            type: 'array',
            title: 'Tags',
            minItems: 1,
            maxItems: 2,
            items: {
              enum: ['alpha', 'beta'],
              enumNames: ['Alpha', 'Beta'],
            },
            default: ['alpha'],
          },
        },
        required: ['name', 'contact'],
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
      settings: {
        allOf: [
          {
            type: 'object',
            properties: {
              retries: {
                type: 'integer',
                title: 'Retries',
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
              },
            },
          },
        ],
      },
    },
    required: ['profile', 'delivery', 'settings'],
  }
}

function makeUnionRetentionSchema() {
  return {
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
  }
}

function makeArraySchema() {
  return {
    type: 'object',
    properties: {
      recipients: {
        type: 'array',
        title: 'Recipients',
        minItems: 1,
        maxItems: 2,
        items: {
          type: 'object',
          title: 'Recipient',
          properties: {
            name: {
              type: 'string',
              title: 'Name',
            },
          },
          required: ['name'],
        },
      },
    },
    required: ['recipients'],
  }
}

function makeOptionalUnionSchema() {
  return {
    type: 'object',
    properties: {
      name: {
        type: 'string',
        title: 'Name',
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
    required: ['name'],
  }
}

function makeLargeChoiceSchema() {
  return {
    type: 'object',
    properties: {
      role: {
        type: 'string',
        title: 'Role',
        enum: ['engineer', 'manager', 'designer', 'director', 'support'],
        enumNames: ['Engineer', 'Manager', 'Designer', 'Director', 'Support'],
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
              },
            },
          },
          {
            title: 'Webhook',
            type: 'object',
            properties: {
              endpoint: {
                type: 'string',
                title: 'Endpoint',
              },
            },
          },
          {
            title: 'SMS',
            type: 'object',
            properties: {
              phone: {
                type: 'string',
                title: 'Phone',
              },
            },
          },
          {
            title: 'Pager',
            type: 'object',
            properties: {
              number: {
                type: 'string',
                title: 'Number',
              },
            },
          },
          {
            title: 'Slack',
            type: 'object',
            properties: {
              channel: {
                type: 'string',
                title: 'Channel',
              },
            },
          },
        ],
      },
    },
    required: ['role', 'delivery'],
  }
}

describe('ElicitationForm', () => {
  it('renders nested controls and submits a recursive payload', async () => {
    const onAccept = vi.fn()

    renderElicitationForm({ onAccept })

    expect(screen.getByText('Profile')).toBeInTheDocument()
    expect(screen.getByText('Contact')).toBeInTheDocument()
    expect(screen.getByText('Choose one option.')).toBeInTheDocument()
    expect(screen.getByText('Choose one or more options.')).toBeInTheDocument()
    expect(screen.getByText('Toggle the requested flag.')).toBeInTheDocument()
    expect(screen.getByText('Enter a numeric value.')).toBeInTheDocument()
    expect(screen.getByText('Choose one branch.')).toBeInTheDocument()
    expect(screen.getByText(/Select at least 1/i)).toBeInTheDocument()
    expect(screen.getByText(/Up to 2 selections/i)).toBeInTheDocument()
    expect(screen.getByRole('switch', { name: /active/i })).toHaveAttribute('data-state', 'checked')
    expect(screen.getByRole('button', { name: /engineer/i })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: /manager/i })).toHaveAttribute('aria-pressed', 'false')
    expect(screen.getByRole('button', { name: /^email$/i })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: /webhook/i })).toHaveAttribute('aria-pressed', 'false')
    expect(screen.getByRole('button', { name: /alpha/i })).toHaveAttribute('data-state', 'on')
    expect(screen.getByRole('button', { name: /beta/i })).toHaveAttribute('data-state', 'off')

    const acceptButton = screen.getByRole('button', { name: /accept and continue/i })
    expect(acceptButton).toBeDisabled()

    fireEvent.change(screen.getByLabelText(/name/i), {
      target: {
        value: 'Ada Lovelace',
      },
    })
    fireEvent.change(screen.getByLabelText(/email/i), {
      target: {
        value: 'ada@example.com',
      },
    })
    fireEvent.click(screen.getByRole('button', { name: /beta/i }))

    fireEvent.click(screen.getByRole('button', { name: /manager/i }))
    fireEvent.click(screen.getByRole('button', { name: /webhook/i }))

    fireEvent.change(screen.getByLabelText(/endpoint/i), {
      target: {
        value: 'https://example.com/hooks',
      },
    })
    fireEvent.change(screen.getByLabelText(/retries/i), {
      target: {
        value: '4',
      },
    })

    expect(acceptButton).toBeEnabled()

    fireEvent.click(acceptButton)

    expect(onAccept).toHaveBeenCalledWith(
      expect.objectContaining({
        profile: expect.objectContaining({
          name: 'Ada Lovelace',
          contact: {
            email: 'ada@example.com',
          },
          active: true,
          role: 'manager',
          tags: expect.arrayContaining(['alpha', 'beta']),
        }),
        delivery: {
          endpoint: 'https://example.com/hooks',
        },
        settings: {
          retries: 4,
          note: 'ship it',
        },
      }),
    )

    expect((onAccept.mock.calls[0]?.[0] as { profile?: { tags?: string[] } }).profile?.tags).toHaveLength(2)
  })

  it('falls back to dropdowns for larger single-choice and union option sets', async () => {
    renderElicitationForm({
      requestedSchema: makeLargeChoiceSchema(),
    })

    expect(screen.getByRole('combobox', { name: /role/i })).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: /branch/i })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /engineer/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /slack/i })).not.toBeInTheDocument()

    await selectOption(/role/i, /director/i)
    await selectOption(/branch/i, /slack/i)

    expect(screen.getByLabelText(/channel/i)).toBeInTheDocument()
  })

  it('accepts manual JSON when the schema falls back to unsupported rendering', () => {
    const onAccept = vi.fn()

    renderElicitationForm({
      onAccept,
      requestedSchema: {
        type: 'object',
        additionalProperties: {
          type: 'string',
        },
        properties: {},
      },
    })

    expect(screen.getByText(/manual json payload/i)).toBeInTheDocument()

    fireEvent.change(screen.getByRole('textbox'), {
      target: { value: '{"email":"ops@example.com"}' },
    })
    fireEvent.click(screen.getByRole('button', { name: /accept and continue/i }))

    expect(onAccept).toHaveBeenCalledWith({
      email: 'ops@example.com',
    })
  })

  it('renders the empty-schema confirmation flow', () => {
    const onAccept = vi.fn()

    renderElicitationForm({
      onAccept,
      requestedSchema: {
        type: 'object',
        properties: {},
      },
    })

    expect(
      screen.getByText('This request only needs confirmation. Accept to continue or decline to stop.'),
    ).toBeInTheDocument()
    expect(screen.getByText('No structured fields were supplied with this request.')).toBeInTheDocument()
    expect(screen.queryByText('Accepting will continue with an empty structured response.')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /accept and continue/i }))

    expect(onAccept).toHaveBeenCalledWith({})
  })

  it('reflects submitting state and forwards decline and cancel actions', () => {
    const onDecline = vi.fn()
    const onCancel = vi.fn()

    renderElicitationForm({
      isSubmitting: true,
      onDecline,
      onCancel,
      requestedSchema: {
        type: 'object',
        properties: {},
      },
    })

    expect(screen.getByRole('button', { name: /submitting/i })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /decline/i }))
    fireEvent.click(screen.getByRole('button', { name: /cancel/i }))

    expect(onDecline).toHaveBeenCalledTimes(1)
    expect(onCancel).toHaveBeenCalledTimes(1)
  })

  it('maps supported string formats to the expected input types', () => {
    renderElicitationForm({
      requestedSchema: {
        type: 'object',
        properties: {
          website: {
            type: 'string',
            title: 'Website',
            format: 'uri',
          },
          start_date: {
            type: 'string',
            title: 'Start date',
            format: 'date',
          },
          scheduled_at: {
            type: 'string',
            title: 'Scheduled at',
            format: 'date-time',
          },
        },
        required: ['website', 'start_date', 'scheduled_at'],
      },
    })

    expect(screen.getByLabelText(/website/i)).toHaveAttribute('type', 'url')
    expect(screen.getByLabelText(/start date/i)).toHaveAttribute('type', 'date')
    expect(screen.getByLabelText(/scheduled at/i)).toHaveAttribute('type', 'datetime-local')
  })

  it('submits local date-time input as RFC3339 content', () => {
    const onAccept = vi.fn()

    renderElicitationForm({
      onAccept,
      requestedSchema: {
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
    })

    fireEvent.change(screen.getByLabelText(/scheduled at/i), {
      target: {
        value: '2026-03-29T10:15',
      },
    })

    fireEvent.click(screen.getByRole('button', { name: /accept and continue/i }))

    expect(onAccept).toHaveBeenCalledWith({
      scheduled_at: new Date('2026-03-29T10:15').toISOString(),
    })
  })

  it('keeps acceptance disabled when the panel is disabled', () => {
    renderElicitationForm({
      disabled: true,
      requestedSchema: {
        type: 'object',
        properties: {},
      },
    })

    expect(screen.getByRole('button', { name: /accept and continue/i })).toBeDisabled()
    expect(screen.getByRole('button', { name: /decline/i })).toBeDisabled()
    expect(screen.getByRole('button', { name: /cancel/i })).toBeDisabled()
  })

  it('preserves union branch drafts when switching branches', async () => {
    const onAccept = vi.fn()

    renderElicitationForm({
      onAccept,
      requestedSchema: makeUnionRetentionSchema(),
    })

    const addressInput = screen.getByLabelText(/address/i)
    fireEvent.change(addressInput, {
      target: {
        value: 'ops@example.com',
      },
    })

    fireEvent.click(screen.getByRole('button', { name: /webhook/i }))

    const endpointInput = screen.getByLabelText(/endpoint/i)
    fireEvent.change(endpointInput, {
      target: {
        value: 'https://example.com/hooks',
      },
    })

    fireEvent.click(screen.getByRole('button', { name: /^email$/i }))
    expect(screen.getByLabelText(/address/i)).toHaveValue('ops@example.com')

    fireEvent.click(screen.getByRole('button', { name: /webhook/i }))
    expect(screen.getByLabelText(/endpoint/i)).toHaveValue('https://example.com/hooks')

    fireEvent.click(screen.getByRole('button', { name: /accept and continue/i }))

    expect(onAccept).toHaveBeenCalledWith({
      delivery: {
        endpoint: 'https://example.com/hooks',
      },
    })
  })

  it('allows optional unions to remain omitted', () => {
    const onAccept = vi.fn()

    renderElicitationForm({
      onAccept,
      requestedSchema: makeOptionalUnionSchema(),
    })

    fireEvent.change(screen.getByLabelText(/name/i), {
      target: {
        value: 'Ada',
      },
    })

    const acceptButton = screen.getByRole('button', { name: /accept and continue/i })
    expect(acceptButton).toBeEnabled()

    fireEvent.click(acceptButton)

    expect(onAccept).toHaveBeenCalledWith({
      name: 'Ada',
    })
  })

  it('adds and removes array items while enforcing validation', () => {
    renderElicitationForm({
      requestedSchema: makeArraySchema(),
    })

    expect(screen.getByText('Add one or more items.')).toBeInTheDocument()
    expect(screen.getByText('At least 1 item. Up to 2 items')).toBeInTheDocument()

    const acceptButton = screen.getByRole('button', { name: /accept and continue/i })
    expect(acceptButton).toBeDisabled()

    expect(screen.getByRole('button', { name: /add item/i })).toBeEnabled()
    fireEvent.click(screen.getByRole('button', { name: /add item/i }))
    fireEvent.click(screen.getByRole('button', { name: /add item/i }))
    expect(screen.getByRole('button', { name: /add item/i })).toBeDisabled()

    const nameInputs = screen.getAllByLabelText(/name/i)
    fireEvent.change(nameInputs[0], {
      target: {
        value: 'Grace',
      },
    })
    fireEvent.change(nameInputs[1], {
      target: {
        value: 'Ada',
      },
    })

    expect(acceptButton).toBeEnabled()

    fireEvent.click(screen.getByRole('button', { name: /remove item 1/i }))

    expect(screen.getAllByLabelText(/name/i)).toHaveLength(1)
    expect(screen.getByLabelText(/name/i)).toHaveValue('Ada')
    expect(screen.getByRole('button', { name: /add item/i })).toBeEnabled()
    expect(acceptButton).toBeEnabled()
  })

  it('shows unsupported-schema fallback for malformed schemas', () => {
    renderElicitationForm({
      requestedSchema: {
        type: 'object',
        properties: {
          nested: {
            type: 'object',
          },
        },
      },
    })

    expect(
      screen.getByText('This request uses a schema shape that Maestro does not render yet. Paste the JSON payload to continue.'),
    ).toBeInTheDocument()
    expect(screen.getAllByText('Nested uses an unsupported object schema shape.')).toHaveLength(1)
    expect(screen.getByText(/manual json payload/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /accept and continue/i })).toBeDisabled()
  })
})
