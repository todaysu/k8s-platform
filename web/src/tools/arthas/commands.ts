export function buildMethodCommand(kind: string, className: string, method: string, threshold: string): string | null {
  // Exact Java identifiers only; inputs must not become Arthas expressions or command separators.
  if (!/^[A-Za-z_$][\w$]*(\.[A-Za-z_$][\w$]*)+$/.test(className)) return null
  if (kind === 'sc') return `sc -d ${className}`
  if (kind === 'jad') return `jad ${className}`
  if (!/^[A-Za-z_$][\w$]*$/.test(method)) return null
  if (kind === 'sm') return `sm -d ${className} ${method}`
  if (kind === 'monitor') return `monitor -c 5 -n 3 ${className} ${method}`
  if (kind === 'watch') return `watch ${className} ${method} throwExp -e -n 5 -x 1`
  if (kind !== 'trace') return null
  if (!/^\d{1,6}$/.test(threshold) || Number(threshold) < 1) return null
  return `trace ${className} ${method} '#cost > ${Number(threshold)}' -n 5`
}

