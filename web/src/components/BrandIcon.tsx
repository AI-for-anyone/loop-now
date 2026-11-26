import { BRAND_ICON_ASSETS, type BrandIconKey } from '@/lib/brand-icons'
import { cn } from '@/lib/utils'

// Official AI harness and model-vendor logo: renders multicolor SVGs via data-inlined <img> (avoiding duplicate SVG <defs> IDs across
// portals/popovers) and monochrome marks via CSS mask-image + currentColor so they adapt automatically to light and dark themes.
export function BrandIcon({ name, className }: { name: BrandIconKey; className?: string }) {
  if (name === 'pi') {
    return (
      <svg
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2.2"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
        data-brand-icon="pi"
        className={cn('size-3.5 shrink-0', className)}
      >
        <path d="M5 7h14" />
        <path d="M9 7v11" />
        <path d="M15 7v8a3 3 0 0 0 3 3" />
      </svg>
    )
  }

  if (name === 'acp') {
    return (
      <svg
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
        data-brand-icon="acp"
        className={cn('size-3.5 shrink-0', className)}
      >
        <rect x="3" y="4" width="18" height="16" rx="3" />
        <path d="m8 10 2.5 2L8 14m4.5 0H16" />
      </svg>
    )
  }

  const asset = BRAND_ICON_ASSETS[name]
  if (!asset) return null

  if (asset.mono) {
    return (
      <span
        aria-hidden="true"
        data-brand-icon={name}
        style={{
          maskImage: `url("${asset.src}")`,
          WebkitMaskImage: `url("${asset.src}")`,
          maskRepeat: 'no-repeat',
          WebkitMaskRepeat: 'no-repeat',
          maskPosition: 'center',
          WebkitMaskPosition: 'center',
          maskSize: 'contain',
          WebkitMaskSize: 'contain',
        }}
        className={cn('inline-block size-3.5 shrink-0 bg-current', className)}
      />
    )
  }

  return (
    <img
      src={asset.src}
      alt=""
      aria-hidden="true"
      data-brand-icon={name}
      className={cn('size-3.5 shrink-0 object-contain', className)}
    />
  )
}
