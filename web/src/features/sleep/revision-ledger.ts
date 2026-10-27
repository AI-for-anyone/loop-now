// Internal UI bookkeeping, independent of the wire schema. API adapters must
// derive owner/scope keys and revisions from generated types, never from names.
export class RevisionLedger<T> {
  private scopes = new Map<string, { revision: number; value: T }>()
  private owners = new Map<string, number>()

  ownerVersion(owner: string): number { return this.owners.get(owner) ?? -1 }

  acceptOwner(owner: string, version: number): boolean {
    if (version < this.ownerVersion(owner)) return false
    this.owners.set(owner, version)
    return true
  }

  observe(scope: string, revision: number, value: T): boolean {
    const known = this.scopes.get(scope)
    if (known && revision <= known.revision) return false
    this.scopes.set(scope, { revision, value })
    return true
  }

  current(scope: string) { return this.scopes.get(scope) }

  // A complete owner snapshot may omit inactive scopes. Keep the scope revision
  // fence even when removing its displayed value; an old event cannot revive it.
  clearFromSnapshot(owner: string, version: number, scope: string, empty: T): boolean {
    if (!this.acceptOwner(owner, version)) return false
    const known = this.scopes.get(scope)
    if (known) this.scopes.set(scope, { ...known, value: empty })
    return true
  }
}
