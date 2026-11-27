import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import { nativeThemeSource, resolveWindowAppearance } from './appearance'
import { parseAppearance } from './bridge'

describe('Desktop appearance parsing and resolution', () => {
  test('parseAppearance accepts valid payloads and drops malformed ones', () => {
    const valid = { skin: 'mono', mode: 'system', resolvedTheme: 'dark', backgroundColor: '#000000' }
    assert.deepEqual(parseAppearance(valid), valid)
    assert.equal(parseAppearance(null), undefined)
    assert.equal(parseAppearance({ ...valid, skin: '' }), undefined)
    assert.equal(parseAppearance({ ...valid, mode: 'auto' }), undefined)
    assert.equal(parseAppearance({ ...valid, resolvedTheme: 'sepia' }), undefined)
    assert.equal(parseAppearance({ ...valid, backgroundColor: '#fff' }), undefined)
    assert.equal(parseAppearance({ ...valid, backgroundColor: 'black' }), undefined)
  })

  test('system mode always follows current OS dark state instead of a stale saved backgroundColor', () => {
    const savedDark = { skin: 'mono', mode: 'system' as const, resolvedTheme: 'dark' as const, backgroundColor: '#000000' }
    assert.deepEqual(resolveWindowAppearance(savedDark, false), {
      skin: 'mono',
      theme: 'light',
      backgroundColor: '#FFFFFF',
    })
    assert.deepEqual(resolveWindowAppearance(savedDark, true), {
      skin: 'mono',
      theme: 'dark',
      backgroundColor: '#000000',
    })
    assert.equal(nativeThemeSource(savedDark), 'system')
  })

  test('explicit light and dark modes ignore OS dark state', () => {
    const light = { skin: 'mono', mode: 'light' as const, resolvedTheme: 'light' as const, backgroundColor: '#FFFFFF' }
    const dark = { skin: 'mono', mode: 'dark' as const, resolvedTheme: 'dark' as const, backgroundColor: '#000000' }
    assert.deepEqual(resolveWindowAppearance(light, true), {
      skin: 'mono',
      theme: 'light',
      backgroundColor: '#FFFFFF',
    })
    assert.deepEqual(resolveWindowAppearance(dark, false), {
      skin: 'mono',
      theme: 'dark',
      backgroundColor: '#000000',
    })
    assert.equal(nativeThemeSource(light), 'light')
    assert.equal(nativeThemeSource(dark), 'dark')
  })

  test('plate skin always resolves to light paper palette regardless of OS dark state', () => {
    const plate = { skin: 'plate', mode: 'system' as const, resolvedTheme: 'light' as const, backgroundColor: '#F3EFE6' }
    assert.deepEqual(resolveWindowAppearance(plate, true), {
      skin: 'plate',
      theme: 'light',
      backgroundColor: '#F3EFE6',
    })
    assert.equal(nativeThemeSource(plate), 'light')
  })

  test('falls back to mono following system mode when no saved appearance exists', () => {
    assert.deepEqual(resolveWindowAppearance(undefined, false), {
      skin: 'mono',
      theme: 'light',
      backgroundColor: '#FFFFFF',
    })
    assert.deepEqual(resolveWindowAppearance(undefined, true), {
      skin: 'mono',
      theme: 'dark',
      backgroundColor: '#000000',
    })
    assert.equal(nativeThemeSource(undefined), 'system')
  })
})
