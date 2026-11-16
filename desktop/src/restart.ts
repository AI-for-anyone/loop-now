// Unexpected exits after the server is ready are restarted by this rule: delays of 1, 5 and 15 seconds in turn, at most 3 times within
// 5 minutes; after one more crash it stops and waits for the user to step in.
// Same rule as the plugin host. Exiting before ready counts as a failed start and does not go through here.
export const restartDelays = [1_000, 5_000, 15_000]
export const restartWindowMs = 5 * 60_000

export class RestartPolicy {
  private crashes: number[] = []

  // Records a crash and returns the milliseconds to wait before restarting; returns null once the limit is exceeded.
  next(now: number): number | null {
    this.crashes = this.crashes.filter((at) => now - at < restartWindowMs)
    this.crashes.push(now)
    return this.crashes.length > restartDelays.length ? null : restartDelays[this.crashes.length - 1]
  }

  // Reset when the user restarts manually.
  reset(): void {
    this.crashes = []
  }
}
