// Branch names are cut in the middle: the end of a name (where tasks' names differ) stays, and the start gives way with an ellipsis.
// Names of up to 24 characters are never split.
const TAIL = 12
export function branchParts(name: string) {
  return name.length <= 2 * TAIL ? { head: '', tail: name } : { head: name.slice(0, -TAIL), tail: name.slice(-TAIL) }
}
