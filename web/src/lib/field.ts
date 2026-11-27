// Shared form control styles: crisp Vercel Geist 1px hairline in mono skin; 2px ink inner border with yellow focus ring in Plate skin.
export const fieldClass =
  'w-full min-w-0 rounded-input border border-hairline-strong bg-surface-control px-3 text-[13.5px] text-ink outline-none transition-[border-color,background-color,box-shadow] duration-150 placeholder:text-ink-500 hover:border-ink/25 focus-visible:border-ink/45 disabled:cursor-not-allowed disabled:opacity-40 aria-invalid:border-red aria-invalid:bg-error-field'

// A native select looks like an input, with a dropdown arrow drawn on the right.
// The arrow icon is defined in select-chevron in the skin CSS (the data URI contains spaces, so it cannot go in a class name).
export const selectClass = `${fieldClass}select-chevron h-9 appearance-none pr-9`
