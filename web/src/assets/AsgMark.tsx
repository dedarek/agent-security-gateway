/** ASG brand mark — a shield + gateway "portal" glyph.
 *  Self-contained inline SVG so it needs no asset pipeline and inherits crisp
 *  rendering at any size. Used top-left in the sidebar as the product identity
 *  (NOT a harness/model logo — ASG is harness-agnostic). */
export function AsgMark({ size = 28 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 40 40" fill="none" xmlns="http://www.w3.org/2000/svg" aria-label="ASG">
      <defs>
        <linearGradient id="asg-g" x1="6" y1="3" x2="34" y2="37" gradientUnits="userSpaceOnUse">
          <stop stopColor="#4b9bff" />
          <stop offset="0.55" stopColor="#165dff" />
          <stop offset="1" stopColor="#0e3fb0" />
        </linearGradient>
        <linearGradient id="asg-core" x1="20" y1="12" x2="20" y2="28" gradientUnits="userSpaceOnUse">
          <stop stopColor="#ffffff" />
          <stop offset="1" stopColor="#cfe2ff" />
        </linearGradient>
      </defs>
      {/* shield silhouette */}
      <path d="M20 2.5 L34.5 7.2 V19.5 C34.5 28.6 28.3 34.9 20 37.5 C11.7 34.9 5.5 28.6 5.5 19.5 V7.2 Z"
            fill="url(#asg-g)" />
      {/* inner gateway ring (the "portal" agents pass through) */}
      <circle cx="20" cy="19" r="8.4" stroke="url(#asg-core)" strokeWidth="2.2" fill="none" opacity="0.92" />
      {/* central checkpoint slit */}
      <rect x="18.4" y="12.2" width="3.2" height="13.6" rx="1.6" fill="url(#asg-core)" />
      {/* pass-through node */}
      <circle cx="20" cy="19" r="2.6" fill="#fff" />
    </svg>
  )
}
