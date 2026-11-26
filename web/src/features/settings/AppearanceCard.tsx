import { useId } from 'react'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { t } from '@/i18n'
import { segmentedClass, segmentedItemClass } from '@/lib/segmented'
import { cn } from '@/lib/utils'
import { skinIds, themeModes, type SkinId, type ThemeMode } from '@/theme/skins'
import { useTheme } from '@/theme/store'

// Miniature visual swatch representing each skin so users can compare palettes before switching.
function SkinThumbnail({ skinId, resolvedTheme }: { skinId: SkinId; resolvedTheme: 'light' | 'dark' }) {
  if (skinId === 'plate') {
    return (
      <span
        aria-hidden="true"
        className="flex h-12 w-full overflow-hidden rounded-ticket border border-[#18265E]/30 bg-[#F3EFE6]"
      >
        <span className="flex w-7 shrink-0 flex-col gap-1 bg-[#2443B5] p-1.5">
          <span className="h-1.5 w-3.5 rounded-xs bg-[#FFE14D]" />
          <span className="h-1 w-4 rounded-xs bg-white/60" />
          <span className="h-1 w-3 rounded-xs bg-white/40" />
        </span>
        <span className="flex flex-1 flex-col justify-between p-2">
          <span className="flex items-center gap-1.5">
            <span className="h-2 w-10 rounded-xs bg-[#18265E]" />
            <span className="size-2 rounded-full bg-[#FF4FA3]" />
          </span>
          <span className="flex items-center gap-1">
            <span className="h-2 w-12 rounded-xs bg-[#E3E8F8]" />
            <span className="h-2 w-5 rounded-full bg-[#FFE14D]" />
          </span>
        </span>
      </span>
    )
  }

  const dark = resolvedTheme === 'dark'
  return (
    <span
      aria-hidden="true"
      className={cn(
        'flex h-12 w-full overflow-hidden rounded-ticket border',
        dark ? 'border-white/15 bg-[#000000]' : 'border-black/12 bg-[#FFFFFF]',
      )}
    >
      <span
        className={cn(
          'flex w-7 shrink-0 flex-col gap-1 border-r p-1.5',
          dark ? 'border-white/10 bg-[#08090B]' : 'border-black/8 bg-[#FAFAFA]',
        )}
      >
        <span className={cn('h-1.5 w-3.5 rounded-xs', dark ? 'bg-[#3291FF]' : 'bg-[#0066DB]')} />
        <span className={cn('h-1 w-4 rounded-xs', dark ? 'bg-white/30' : 'bg-black/20')} />
        <span className={cn('h-1 w-3 rounded-xs', dark ? 'bg-white/20' : 'bg-black/15')} />
      </span>
      <span className={cn('flex flex-1 flex-col justify-between p-2', dark ? 'bg-[#0B0C0F]' : 'bg-[#FAFAFA]')}>
        <span className="flex items-center gap-1.5">
          <span className={cn('h-2 w-10 rounded-xs', dark ? 'bg-[#EDEDED]' : 'bg-[#171717]')} />
          <span className={cn('size-2 rounded-full', dark ? 'bg-[#2DD4BF]' : 'bg-[#0D9488]')} />
        </span>
        <span className="flex items-center gap-1">
          <span className={cn('h-2 w-12 rounded-xs border', dark ? 'border-white/10 bg-[#191B21]' : 'border-black/8 bg-[#FFFFFF]')} />
          <span className={cn('h-2 w-5 rounded-full', dark ? 'bg-[#3291FF]' : 'bg-[#0066DB]')} />
        </span>
      </span>
    </span>
  )
}

export function AppearanceCard({ labelledBy }: { labelledBy: string }) {
  const { skin, mode, resolvedTheme, supportsDark, setSkin, setThemeMode } = useTheme()
  const copy = t.settings.appearance
  const hintId = useId()
  const skinLabelId = useId()
  const modeLabelId = useId()

  return (
    <div className="grid gap-4">
      <div className="grid gap-2">
        <span id={skinLabelId} className="text-[12.5px] leading-4 font-medium text-ink-700 [[data-skin=plate]_&]:text-[13px] [[data-skin=plate]_&]:font-semibold">
          {copy.skinLabel}
        </span>
        <div
          role="radiogroup"
          aria-labelledby={`${labelledBy} ${skinLabelId}`}
          aria-describedby={hintId}
          className="grid gap-2.5 sm:grid-cols-2"
        >
          {skinIds.map((id) => {
            const active = skin === id
            const meta = copy.skins[id]
            return (
              <button
                key={id}
                type="button"
                role="radio"
                aria-checked={active}
                data-skin-option={id}
                onClick={() => setSkin(id)}
                className={cn(
                  'flex cursor-pointer flex-col items-start gap-2.5 rounded-card border p-3 text-left transition-[background-color,border-color,box-shadow] duration-150 outline-none focus-visible:shadow-focus',
                  active
                    ? 'border-riso-blue bg-tint-blue/40 shadow-[0_0_0_1px_var(--riso-blue)] [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:shadow-[3px_3px_0_var(--riso-pink)]'
                    : 'border-hairline bg-surface-control hover:border-hairline-strong hover:bg-surface-subtle',
                )}
              >
                <SkinThumbnail skinId={id} resolvedTheme={resolvedTheme} />
                <div className="grid gap-0.5">
                  <span className="text-[13.5px] font-medium text-ink [[data-skin=plate]_&]:font-black">{meta.name}</span>
                  <span className="text-[12.5px] leading-snug text-ink-500">{meta.description}</span>
                </div>
              </button>
            )
          })}
        </div>
      </div>

      <div className="grid gap-2">
        <span id={modeLabelId} className="text-[12.5px] leading-4 font-medium text-ink-700 [[data-skin=plate]_&]:text-[13px] [[data-skin=plate]_&]:font-semibold">
          {copy.modeLabel}
        </span>
        <div className="flex flex-wrap items-center gap-3">
          <ToggleGroup
            aria-labelledby={modeLabelId}
            className={cn(segmentedClass, 'h-9 justify-self-start', !supportsDark && 'opacity-50')}
            value={supportsDark ? mode : 'light'}
            onValueChange={(value) => {
              if (supportsDark && value) setThemeMode(value as ThemeMode)
            }}
          >
            {themeModes.map((value) => (
              <ToggleGroupItem
                key={value}
                value={value}
                disabled={!supportsDark}
                className={segmentedItemClass}
              >
                {copy.modes[value]}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
          {!supportsDark && <span className="text-[12.5px] text-ink-500">{copy.lightOnlyHint}</span>}
        </div>
      </div>

      <p id={hintId} className="text-[13px] text-ink-700">
        {copy.hint}
      </p>
    </div>
  )
}
