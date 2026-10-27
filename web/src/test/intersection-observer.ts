// jsdom 没有 IntersectionObserver。这个假实现记下所有观察者，测试可以手动触发某个元素进入或离开视口。
export class FakeIntersectionObserver {
  static instances: FakeIntersectionObserver[] = []
  readonly targets = new Set<Element>()
  readonly callback: IntersectionObserverCallback

  constructor(callback: IntersectionObserverCallback) {
    this.callback = callback
    FakeIntersectionObserver.instances.push(this)
  }

  observe(target: Element) {
    this.targets.add(target)
  }

  unobserve(target: Element) {
    this.targets.delete(target)
  }

  disconnect() {
    this.targets.clear()
  }

  takeRecords(): IntersectionObserverEntry[] {
    return []
  }

  // 让观察着 target 的观察者收到一次进入或离开视口的通知。
  static trigger(target: Element, isIntersecting: boolean) {
    for (const observer of FakeIntersectionObserver.instances) {
      if (!observer.targets.has(target)) continue
      const entry = { target, isIntersecting, intersectionRatio: isIntersecting ? 1 : 0 } as IntersectionObserverEntry
      observer.callback([entry], observer as unknown as IntersectionObserver)
    }
  }

  static reset() {
    FakeIntersectionObserver.instances = []
  }
}
