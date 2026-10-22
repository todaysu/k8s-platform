import { useEffect, useState } from 'react'
import { fetchPlatformSettings, type PlatformSettings, type SettingsSection } from '../api/settings'

export function isPlatformSettings(value: unknown): value is PlatformSettings {
  if (!value || typeof value !== 'object') return false
  const candidate = value as Partial<PlatformSettings>
  return Boolean(candidate.security && typeof candidate.security === 'object' && candidate.java && typeof candidate.java === 'object')
}

export function usePlatformSettings() {
  const [settings, setSettings] = useState<PlatformSettings | null>(null)
  useEffect(() => {
    let active = true
    void fetchPlatformSettings().then((value) => {
      if (active && isPlatformSettings(value)) setSettings(value)
    }).catch(() => {
      // A settings read failure must not block the feature page; the page
      // simply cannot show an actionable configuration warning.
    })
    return () => { active = false }
  }, [])
  return settings
}

export function openPlatformSettings(section: SettingsSection) {
  window.history.pushState({ managementTab: 'parameters', settings: section }, '', `/settings/access?tab=parameters&section=${section}`)
  window.dispatchEvent(new PopStateEvent('popstate'))
}
