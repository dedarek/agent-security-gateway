import { motion } from 'motion/react'
import { useEffect, useRef, useState, type ReactNode } from 'react'

/** CountUp — 数字滚动动画（KPI 用）。遵守 prefers-reduced-motion。 */
export function CountUp({ value, duration = 900, className, style }: { value: number; duration?: number; className?: string; style?: React.CSSProperties }) {
  const [display, setDisplay] = useState(0)
  const fromRef = useRef(0)
  const rafRef = useRef<number | null>(null)

  useEffect(() => {
    if (typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) {
      setDisplay(value); return
    }
    const from = fromRef.current
    const to = value
    const t0 = performance.now()
    const tick = (now: number) => {
      const p = Math.min((now - t0) / duration, 1)
      const eased = 1 - Math.pow(1 - p, 3) // easeOutCubic
      setDisplay(Math.round(from + (to - from) * eased))
      if (p < 1) rafRef.current = requestAnimationFrame(tick)
      else fromRef.current = to
    }
    rafRef.current = requestAnimationFrame(tick)
    return () => { if (rafRef.current) cancelAnimationFrame(rafRef.current) }
  }, [value, duration])

  return <span className={className} style={style}>{display.toLocaleString()}</span>
}

/** FadeInCard — 卡片渐入上浮。index 控制错峰。 */
export function FadeInCard({ children, index = 0, className, style }: { children: ReactNode; index?: number; className?: string; style?: React.CSSProperties }) {
  return (
    <motion.div
      className={className}
      style={style}
      initial={{ opacity: 0, y: 14 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.42, delay: index * 0.06, ease: [0.22, 1, 0.36, 1] }}
    >
      {children}
    </motion.div>
  )
}

/** AnimatedBar — 进度条宽度从 0 生长。 */
export function AnimatedBar({ pct, color, className }: { pct: number; color?: string; className?: string }) {
  return (
    <motion.div
      className={className}
      style={{ background: color }}
      initial={{ width: 0 }}
      animate={{ width: `${pct}%` }}
      transition={{ duration: 0.7, ease: [0.22, 1, 0.36, 1] }}
    />
  )
}

export function Stagger({ children, className, style }: { children: ReactNode; className?: string; style?: React.CSSProperties }) {
  return (
    <motion.div
      className={className}
      style={style}
      initial="hidden"
      animate="show"
      variants={{ hidden: {}, show: { transition: { staggerChildren: 0.07 } } }}
    >
      {children}
    </motion.div>
  )
}

/** StaggerItem — Stagger 的子项。 */
export function StaggerItem({ children, className, style }: { children: ReactNode; className?: string; style?: React.CSSProperties }) {
  return (
    <motion.div
      className={className}
      style={style}
      variants={{ hidden: { opacity: 0, x: -12 }, show: { opacity: 1, x: 0, transition: { duration: 0.34, ease: [0.22, 1, 0.36, 1] } } }}
    >
      {children}
    </motion.div>
  )
}
