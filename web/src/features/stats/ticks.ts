// 纵轴刻度：从 0 到不小于 max 的整齐数，大约 count 格。steps 是每格可取的首位数。
export function niceTicks(max: number, count: number, steps = [1, 2, 2.5, 3, 5]): number[] {
  const top = max > 0 ? max : 1
  const raw = top / count
  const magnitude = 10 ** Math.floor(Math.log10(raw))
  const step = (steps.find((s) => s * magnitude >= raw - 1e-9) ?? 10) * magnitude
  const n = Math.max(1, Math.ceil(top / step - 1e-9))
  return Array.from({ length: n + 1 }, (_, i) => i * step)
}

// 时长的刻度取整秒、整分或整小时。
const DURATION_STEPS = [1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400]
export function durationTicks(max: number, count: number): number[] {
  const top = max > 0 ? max : 1
  const raw = top / count
  const step = DURATION_STEPS.find((s) => s >= raw) ?? Math.ceil(raw / 86400) * 86400
  const n = Math.max(1, Math.ceil(top / step - 1e-9))
  return Array.from({ length: n + 1 }, (_, i) => i * step)
}
