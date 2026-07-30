import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { SameAPIModelSelector } from './SameAPIModelSelector'

describe('SameAPIModelSelector', () => {
  it('excludes the primary model and selects discovered sibling models in order', () => {
    const onChange = vi.fn()
    render(
      <SameAPIModelSelector
        models={['gpt-5.6-sol', 'gpt-5.6-terra', 'gpt-5.6-luna']}
        primaryModelName="gpt-5.6-sol"
        selectedModelNames={[]}
        onChange={onChange}
        language="en"
      />
    )

    expect(screen.queryByText('gpt-5.6-sol')).not.toBeInTheDocument()
    fireEvent.click(screen.getAllByRole('checkbox')[0])
    expect(onChange).toHaveBeenCalledWith(['gpt-5.6-terra'])
  })

  it('keeps a previously saved model visible when catalog refresh fails', () => {
    render(
      <SameAPIModelSelector
        models={[]}
        primaryModelName="gpt-5.6-sol"
        selectedModelNames={['gpt-5.6-terra']}
        onChange={() => undefined}
        language="en"
        error="Catalog unavailable"
      />
    )
    expect(screen.getByText('gpt-5.6-terra')).toBeVisible()
    expect(screen.getByText('Catalog unavailable')).toBeVisible()
  })
})
