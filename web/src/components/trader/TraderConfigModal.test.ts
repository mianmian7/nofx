import { describe, expect, it } from 'vitest'
import { getAIModelOptionLabel } from './TraderConfigModal'

describe('getAIModelOptionLabel', () => {
  it('shows both the API configuration and its primary model', () => {
    expect(
      getAIModelOptionLabel({
        id: 'user-openai',
        name: 'OpenAI AI',
        provider: 'openai',
        enabled: true,
        customModelName: 'gpt-5.6-sol',
      })
    ).toBe('OPENAI AI · gpt-5.6-sol')
  })
})
