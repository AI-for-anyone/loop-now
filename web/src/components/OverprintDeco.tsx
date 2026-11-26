import { cn } from '@/lib/utils'

// The decoration in the top right of the page header: a patch of halftone dots, three overprinted circles in blue, pink and yellow, and a
// registration mark in the top left. Purely decorative: screen readers skip it and it does not block clicks.
// Put it inside the header: the header must be a positioned container and also a size container (@container). It is hidden when the
// header is narrower than 820px (narrow screens, half-screen windows);
// while it is shown, each line of text in the header makes room for it with @[820px]:pr-[316px], so long names wrap instead of running
// over the circles.
// compact: in an Agent's compact header (wide screens) the whole thing shrinks to 0.6x and hugs the top right corner, matching the header's
// height; narrow screens are unchanged.
export function OverprintDeco({ className, compact = false }: { className?: string; compact?: boolean }) {
  return (
    <div
      aria-hidden="true"
      data-overprint-deco
      className={cn('pointer-events-none absolute top-5 right-3.5 hidden h-[132px] w-[300px] origin-top-right nav:right-6 @[820px]:block', compact && 'nav:top-2 nav:scale-[0.6]', className)}
    >
      <span className="absolute top-[34px] left-0 h-[84px] w-[150px] bg-[radial-gradient(var(--ink)_1.7px,transparent_2.1px)] bg-size-[9px_9px] opacity-55" />
      <span className="absolute top-2 left-[70px] size-24 rounded-full bg-riso-blue mix-blend-multiply" />
      <span className="absolute top-[26px] left-[132px] size-24 rounded-full bg-riso-pink mix-blend-multiply" />
      <span className="absolute top-2 left-[194px] size-24 rounded-full bg-riso-yellow mix-blend-multiply" />
      {/* Size is set in class names (not the width attribute), so it is not mistaken for a chart on the page. */}
      <svg viewBox="0 0 18 18" fill="none" stroke="var(--ink)" strokeWidth="1.2" className="absolute top-1 left-5 size-[18px]">
        <circle cx="9" cy="9" r="5" />
        <path d="M9 0v18M0 9h18" />
      </svg>
    </div>
  )
}
