import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import App from './App'

describe('App', () => {
  it('shows the platform title', () => {
    render(<App />)

    expect(
      screen.getByRole('heading', { name: 'Kubernetes 多集群管理平台' }),
    ).toBeInTheDocument()
  })
})
