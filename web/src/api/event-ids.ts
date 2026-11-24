// Compare two event stream entry IDs (like 1700000000000-0). Compared as strings when the format does not match.
export function compareEventIds(a: string, b: string): number {
  const pa = /^(\d+)-(\d+)$/.exec(a)
  const pb = /^(\d+)-(\d+)$/.exec(b)
  if (!pa || !pb) return a === b ? 0 : a < b ? -1 : 1
  const ms = BigInt(pa[1]) - BigInt(pb[1])
  if (ms !== 0n) return ms < 0n ? -1 : 1
  const seq = BigInt(pa[2]) - BigInt(pb[2])
  return seq === 0n ? 0 : seq < 0n ? -1 : 1
}
