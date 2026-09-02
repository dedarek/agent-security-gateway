import openai from './01_openai.svg'
import anthropic from './02_anthropic.svg'
import google from './03_google.svg'
import grok from './04_grok.svg'
import zhipu from './05_zhipu.svg'
import kimi from './06_kimi.svg'
import qwen from './07_qwen.svg'
import minimax from './08_minimax.svg'
import deepseek from './09_deepseek.svg'
import opencode from './10_opencode.svg'
import pi from './11_pi_inflection.svg'
import openclaw from './12_openclaw.svg'
import hermes from './13_hermes_agent.svg'
import claudeCode from './14_claude_code.svg'
import tencent from './15_tencent_hunyuan.svg'

const MAP: Record<string, string> = {
  openai, anthropic, google, grok, zhipu, zhipu_ai: zhipu,
  kimi, qwen, minimax, deepseek,
  opencode, pi, inflection: pi,
  openclaw, hermes, hermes_agent: hermes,
  claude_code: claudeCode, 'claude-code': claudeCode, claudecode: claudeCode,
  // Codex is an OpenAI agent; keep the agent identity logo independent from
  // the model field, which is intentionally empty for ChatGPT private login.
  codex: openai, openai_codex: openai, 'openai-codex': openai,
  tencent, hunyuan: tencent, tencent_hunyuan: tencent, 'tencent-hunyuan': tencent,
  hy3: tencent, 'hy3_paid': tencent, hy4: tencent, hunyuan_hy3: tencent,
}

function normalize(k: string): string {
  return k.toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '')
}

export function logoFor(key?: string | null): string | null {
  if (!key) return null
  const n = normalize(key)
  // direct hit
  if (MAP[n]) return MAP[n]
  // substring hit: e.g. "opencode-zen" -> opencode, "qwen38-27b" -> qwen, "hermes-2" -> hermes
  for (const [k, v] of Object.entries(MAP)) {
    if (n.includes(k) || k.includes(n)) return v
  }
  // alias: zhipu family, google/gemini
  if (n.includes('gemini')) return google
  if (n.includes('zhipu') || n.includes('glm')) return zhipu
  if (n.includes('moonshot')) return kimi
  return null
}

export function BrandLogo({ name, size = 14 }: { name: string; size?: number }) {
  const src = logoFor(name)
  if (!src) return null
  return <img src={src} alt={name} width={size} height={size} style={{ objectFit: 'contain', flexShrink: 0 }} loading="lazy" />
}

export function BrandChip({ name, size = 14, style }: { name: string; size?: number; style?: React.CSSProperties }) {
  const src = logoFor(name)
  if (!src) return <span className="chip">{name}</span>
  return (
    <span className="chip" style={{ gap: 5, padding: '3px 8px 3px 5px', ...style }}>
      <img src={src} alt={name} width={size} height={size} style={{ objectFit: 'contain', flexShrink: 0 }} loading="lazy" />
      {name}
    </span>
  )
}
