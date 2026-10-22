import { Alert, AlertActionLink } from '@patternfly/react-core'
import type { ReactNode } from 'react'
import type { SettingsSection } from '../api/settings'
import { openPlatformSettings } from '../pages/usePlatformSettings'

type Props = {
  children: ReactNode
  section: SettingsSection
  title: string
  variant?: 'danger' | 'info' | 'warning'
}

export default function SettingsRequirementAlert({ children, section, title, variant = 'warning' }: Props) {
  return <Alert
    actionLinks={<AlertActionLink onClick={() => openPlatformSettings(section)}>前往参数配置</AlertActionLink>}
    isInline
    title={title}
    variant={variant}
  >
    {children}
  </Alert>
}
