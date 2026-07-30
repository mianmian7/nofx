import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../lib/api'
import { ModelConfigModal } from './ModelConfigModal'

vi.mock('../../lib/api', () => ({
  api: {
    discoverAIModels: vi.fn(),
  },
}))

describe('ModelConfigModal model catalog', () => {
  beforeEach(() => {
    vi.mocked(api.discoverAIModels).mockReset()
  })

  it('selects only the primary model after discovery and supports batch selection', async () => {
    vi.mocked(api.discoverAIModels).mockResolvedValue([
      'gpt-5.6-sol',
      'gpt-5.6-terra',
      'gpt-5.6-luna',
    ])
    const onSave = vi.fn()
    render(
      <ModelConfigModal
        allModels={[]}
        configuredModels={[
          {
            id: 'user-openai',
            name: 'OpenAI',
            provider: 'openai',
            enabled: true,
            has_api_key: true,
            customApiUrl: 'https://example.com/v1',
            customModelName: 'gpt-5.6-sol',
          },
        ]}
        editingModelId="user-openai"
        onSave={onSave}
        onDelete={() => undefined}
        onClose={() => undefined}
        language="en"
      />
    )

    fireEvent.click(screen.getByRole('button', { name: 'Load models' }))
    await waitFor(() => expect(screen.getByText('gpt-5.6-terra')).toBeVisible())
    expect(api.discoverAIModels).toHaveBeenCalledWith({
      model_id: 'user-openai',
      provider: 'openai',
      api_key: '',
      custom_api_url: 'https://example.com/v1',
    })

    const checkboxes = screen.getAllByRole('checkbox')
    expect(checkboxes[0]).toBeChecked()
    expect(checkboxes[1]).not.toBeChecked()
    expect(checkboxes[2]).not.toBeChecked()
    expect(screen.getByRole('button', { name: 'Select all' })).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Invert selection' }))
    expect(checkboxes[0]).not.toBeChecked()
    expect(checkboxes[1]).toBeChecked()
    expect(checkboxes[2]).toBeChecked()
    fireEvent.click(checkboxes[0])
    fireEvent.click(screen.getAllByRole('radio')[0])
    fireEvent.click(screen.getByRole('button', { name: 'Save Configuration' }))

    expect(onSave).toHaveBeenCalledWith(
      'user-openai',
      '',
      'https://example.com/v1',
      'gpt-5.6-sol',
      ['gpt-5.6-terra', 'gpt-5.6-luna', 'gpt-5.6-sol']
    )
  })

  it('prefers persisted configuration details over the supported-model template when editing', () => {
    render(
      <ModelConfigModal
        allModels={[
          {
            id: 'openai',
            name: 'OpenAI template',
            provider: 'openai',
            enabled: false,
          },
        ]}
        configuredModels={[
          {
            id: 'openai',
            name: 'OpenAI API',
            provider: 'openai',
            enabled: true,
            has_api_key: true,
            customApiUrl: 'https://example.com/v1',
            customModelName: 'gpt-5.6-sol',
            modelNames: ['gpt-5.6-sol', 'gpt-5.6-terra', 'gpt-5.6-luna'],
          },
        ]}
        editingModelId="openai"
        onSave={() => undefined}
        onDelete={() => undefined}
        onClose={() => undefined}
        language="en"
      />
    )

    expect(screen.getByDisplayValue('https://example.com/v1')).toBeVisible()
    expect(screen.getByText('gpt-5.6-terra')).toBeVisible()
    expect(screen.getByText('gpt-5.6-luna')).toBeVisible()
    expect(screen.getByText('Current primary:')).toHaveTextContent(
      'Current primary: gpt-5.6-sol'
    )
  })
})
