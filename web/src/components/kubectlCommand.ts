export function parseKubectlCommand(command: string): string[] {
 const args: string[] = []; let value = ''; let quote = ''; let escape = false; let started = false
 for (const char of command.trim()) {
  if (escape) { value += char; escape = false; started = true; continue }
  if (char === '\\' && quote !== "'") { escape = true; started = true; continue }
  if (quote) { if (char === quote) quote = ''; else value += char; continue }
  if (char === '"' || char === "'") { quote = char; started = true; continue }
  if ('|;&`\n\r'.includes(char) || char === '$' || char === '<' || char === '>') throw new Error('请直接输入 kubectl 命令，不支持 Shell 管道或重定向。')
  if (/\s/.test(char)) { if (started) { args.push(value); value = ''; started = false }; continue }
  value += char; started = true
 }
 if (quote || escape) throw new Error('引号或转义未结束。')
 if (started) args.push(value)
 if (args[0] === 'kubectl') args.shift()
 if (!args.length) throw new Error('请输入 kubectl 命令。')
 return args
}
