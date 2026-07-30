import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { FallbackModelSelector } from './FallbackModelSelector'

const models = [
  {
    id: 'primary',
    name: 'Primary',
    provider: 'openai',
    enabled: true,
  },
  {
    id: 'fallback',
    name: 'Fallback',
    provider: 'deepseek',
    customModelName: 'deepseek-chat',
    enabled: true,
  },
  {
    id: 'disabled',
    name: 'Disabled',
    provider: 'qwen',
    enabled: false,
  },
]

describe('FallbackModelSelector', () => {
  it('only offers enabled configured models other than the primary model', () => {
    render(
      <FallbackModelSelector
        models={models}
        primaryModelId="primary"
        selectedModelIds={[]}
        onChange={() => undefined}
        language="en"
      />
    )

    expect(screen.getByText(/Fallback · deepseek/)).toBeVisible()
    expect(screen.queryByText(/Primary · openai/)).not.toBeInTheDocument()
    expect(screen.queryByText(/Disabled · qwen/)).not.toBeInTheDocument()
  })

  it('returns configured model IDs in selection order', () => {
    const onChange = vi.fn()
    render(
      <FallbackModelSelector
        models={models}
        primaryModelId="primary"
        selectedModelIds={[]}
        onChange={onChange}
        language="en"
      />
    )

    fireEvent.click(screen.getByRole('checkbox'))
    expect(onChange).toHaveBeenCalledWith(['fallback'])
  })
})
