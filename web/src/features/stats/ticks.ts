// Y-axis ticks: from 0 up to a round number no smaller than max, in about count steps. steps lists the allowed leading
// digits of each step.
export function niceTicks(max: number, count: number, steps = [1, 2, 2.5, 3, 5]): number[] {
  const top = max > 0 ? max : 1
  const raw = top / count
  const magnitude = 10 ** Math.floor(Math.log10(raw))
  const step = (steps.find((s) => s * magnitude >= raw - 1e-9) ?? 10) * magnitude
  const n = Math.max(1, Math.ceil(top / step - 1e-9))
  return Array.from({ length: n + 1 }, (_, i) => i * step)
}

// Duration ticks are whole seconds, whole minutes or whole hours.
const DURATION_STEPS = [1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400]
export function durationTicks(max: number, count: number): number[] {
  const top = max > 0 ? max : 1
  const raw = top / count
  const step = DURATION_STEPS.find((s) => s >= raw) ?? Math.ceil(raw / 86400) * 86400
  const n = Math.max(1, Math.ceil(top / step - 1e-9))
  return Array.from({ length: n + 1 }, (_, i) => i * step)
}
