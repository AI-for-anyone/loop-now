// 服务端就绪之后的意外退出按这个规则重启：间隔依次 1、5、15 秒，5 分钟内最多 3 次，再崩溃就停下等人处理。
// 与插件宿主的规则一致。还没就绪就退出算启动失败，不走这里。
export const restartDelays = [1_000, 5_000, 15_000]
export const restartWindowMs = 5 * 60_000

export class RestartPolicy {
  private crashes: number[] = []

  // 记一次崩溃，返回重启前要等的毫秒数；超过上限时返回 null。
  next(now: number): number | null {
    this.crashes = this.crashes.filter((at) => now - at < restartWindowMs)
    this.crashes.push(now)
    return this.crashes.length > restartDelays.length ? null : restartDelays[this.crashes.length - 1]
  }

  // 人手动重启时清零。
  reset(): void {
    this.crashes = []
  }
}
