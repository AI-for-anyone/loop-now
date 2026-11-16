// Write text to the clipboard; returns false instead of throwing when it cannot be written.
// navigator.clipboard only exists in secure contexts (not when the console is opened from a LAN address over http), and Safari only allows
// writing inside a key press or click gesture;
// writing after a network request comes back is refused. In both cases fall back to the old copy command (which can also fail for not
// being inside a gesture).
export async function copyText(text: string): Promise<boolean> {
  try {
    if (typeof navigator.clipboard?.writeText === 'function') {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // Refused by the browser: try the old copy command.
  }
  return legacyCopy(text)
}

function legacyCopy(text: string): boolean {
  if (typeof document.execCommand !== 'function') return false
  const focused = document.activeElement instanceof HTMLElement ? document.activeElement : null
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.setAttribute('aria-hidden', 'true')
  area.style.cssText = 'position:fixed;top:0;left:0;opacity:0;pointer-events:none'
  document.body.appendChild(area)
  // The copy command copies the current selection: the temporary text area must take focus and select all first, and focus is given back
  // afterwards.
  area.focus({ preventScroll: true })
  area.select()
  try {
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    area.remove()
    focused?.focus({ preventScroll: true })
  }
}
