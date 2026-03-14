import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { useState } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { IssueDescriptionField } from '@/components/issue-description-field'
import { MockSpeechRecognition } from '@/test/mock-speech-recognition'
import { renderWithQueryClient } from '@/test/test-utils'

function Harness({ initialValue = '' }: { initialValue?: string }) {
  const [value, setValue] = useState(initialValue)

  return (
    <>
      <span id="description-label">Description</span>
      <IssueDescriptionField labelledBy="description-label" value={value} onChange={setValue} />
    </>
  )
}

describe('IssueDescriptionField', () => {
  beforeEach(() => {
    MockSpeechRecognition.reset()
    vi.unstubAllGlobals()
  })

  it('hides the speech control when browser speech input is unavailable', () => {
    renderWithQueryClient(<Harness />)

    expect(screen.queryByRole('button', { name: /speech to text/i })).not.toBeInTheDocument()
  })

  it('shows live interim text, locks typing, and keeps finalized speech when the session ends', async () => {
    vi.stubGlobal('SpeechRecognition', MockSpeechRecognition)

    renderWithQueryClient(<Harness initialValue="Existing summary" />)

    fireEvent.click(screen.getByRole('button', { name: /start speech to text/i }))

    const recognition = MockSpeechRecognition.instances[0]
    expect(recognition).toBeDefined()

    const textarea = screen.getByLabelText(/description/i)
    expect(textarea).toHaveAttribute('readonly')
    expect(screen.getByRole('button', { name: /stop speech to text/i })).toBeInTheDocument()
    expect(screen.getByTestId('issue-speech-visualizer')).toBeInTheDocument()

    await act(async () => {
      recognition.emitResult([
        { transcript: 'confirmed fix', isFinal: true },
        { transcript: ' adding test coverage', isFinal: false },
      ])
    })

    await waitFor(() => {
      expect(textarea).toHaveValue('Existing summary confirmed fix adding test coverage')
    })

    await act(async () => {
      recognition.stop()
    })

    await waitFor(() => {
      expect(textarea).toHaveValue('Existing summary confirmed fix')
    })
    expect(textarea).not.toHaveAttribute('readonly')
  })

  it('shows an inline error when recognition fails', async () => {
    vi.stubGlobal('SpeechRecognition', MockSpeechRecognition)

    renderWithQueryClient(<Harness />)

    fireEvent.click(screen.getByRole('button', { name: /start speech to text/i }))

    const recognition = MockSpeechRecognition.instances[0]
    await act(async () => {
      recognition.emitError('not-allowed')
    })

    await waitFor(() => {
      expect(screen.getByText(/microphone access was denied/i)).toBeInTheDocument()
    })
    expect(screen.getByRole('button', { name: /start speech to text/i })).toBeInTheDocument()
  })
})
