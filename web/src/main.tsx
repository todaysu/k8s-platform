import '@patternfly/react-core/dist/styles/base.css'
import { StrictMode, useEffect, useState } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { AuthGate } from './auth/Auth'
import SetupPage from './setup/SetupPage'

function Root() {
  const [setupRequired, setSetupRequired] = useState<boolean | null>(null)

  useEffect(() => {
    let active = true
    void fetch('/api/v1/setup/status')
      .then(async response => {
        if (!response.ok) return false
        const payload = await response.json()
        return Boolean(payload?.data?.setup_required)
      })
      .then(value => { if (active) setSetupRequired(value) })
      .catch(() => { if (active) setSetupRequired(false) })
    return () => { active = false }
  }, [])

  if (setupRequired === null) {
    return <main className="ks-login"><p>正在检查平台初始化状态…</p></main>
  }
  if (setupRequired) {
    return <SetupPage />
  }
  return <AuthGate><App /></AuthGate>
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Root />
  </StrictMode>,
)
