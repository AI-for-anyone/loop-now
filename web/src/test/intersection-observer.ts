// jsdom has no IntersectionObserver. This fake records every observer, so tests can manually make an element enter or leave the viewport.
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

  // Sends the observers watching target one notification that it entered or left the viewport.
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
