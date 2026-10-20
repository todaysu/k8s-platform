import './PlatformBrand.css'

export const PLATFORM_NAME = 'FTC'
export const PLATFORM_FULL_NAME = 'FTC 集群运维平台'

export function PlatformLogoMark({ className = '' }: { className?: string }) {
  return (
    <span className={`ks-platform-logo ${className}`.trim()} aria-hidden="true">
      <svg viewBox="0 0 40 40" role="img">
        <path d="M20 9.2 10.5 14.7v10.9L20 31l9.5-5.4V14.7L20 9.2Z" fill="none" stroke="currentColor" strokeWidth="2.2" />
        <path d="M20 13.2v6.7m0 0-5.9 3.4m5.9-3.4 5.9 3.4" fill="none" stroke="currentColor" strokeLinecap="round" strokeWidth="2.2" />
        <circle cx="20" cy="10.6" r="3.1" fill="currentColor" />
        <circle cx="11.7" cy="25.4" r="3.1" fill="currentColor" />
        <circle cx="28.3" cy="25.4" r="3.1" fill="currentColor" />
        <circle cx="20" cy="20" r="2.4" fill="currentColor" />
      </svg>
    </span>
  )
}

export function PlatformBrand({ variant = 'masthead' }: { variant?: 'masthead' | 'login' }) {
  return (
    <span className={`ks-platform-brand ks-platform-brand--${variant}`}>
      <PlatformLogoMark />
      <span className="ks-platform-brand__copy">
        <strong>{PLATFORM_NAME}</strong>
        <small>集群运维平台</small>
      </span>
    </span>
  )
}
