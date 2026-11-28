import { useMemo } from 'react'
import { encode } from 'uqr'
import { cn } from '@/lib/utils'

// Render a standard ISO/IEC 18004 QR code as a single vector path with a 4-module quiet zone on a white background so cameras scan
// reliably in both light and dark themes.
export function QrCode({ value, label, className }: { value: string; label: string; className?: string }) {
  const { size, path } = useMemo(() => {
    const qr = encode(value, { ecc: 'M', border: 4 })
    const segments: string[] = []
    for (let y = 0; y < qr.size; y++) {
      const row = qr.data[y]
      if (!row) continue
      let x = 0
      while (x < qr.size) {
        if (!row[x]) {
          x += 1
          continue
        }
        const start = x
        while (x < qr.size && row[x]) x += 1
        segments.push(`M${start} ${y}h${x - start}v1h-${x - start}z`)
      }
    }
    return { size: qr.size, path: segments.join('') }
  }, [value])

  return (
    <svg
      role="img"
      aria-label={label}
      viewBox={`0 0 ${size} ${size}`}
      shapeRendering="crispEdges"
      className={cn('block size-[220px] max-w-full rounded-[6px] bg-white', className)}
    >
      <rect width={size} height={size} fill="#ffffff" />
      <path d={path} fill="#000000" />
    </svg>
  )
}
