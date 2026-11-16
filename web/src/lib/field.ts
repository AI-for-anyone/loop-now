// The look of inputs: white background, 12 corner radius, 2px navy inner border (every form control in the redesign drafts looks like
// this); a yellow outer ring on focus; on error (aria-invalid), a light red background with a vermilion inner border.
export const fieldClass =
  'w-full min-w-0 rounded-input border-0 bg-white px-3.5 text-[14.5px] text-ink shadow-[inset_0_0_0_2px_var(--ink)] outline-none placeholder:text-ink-500 focus-visible:shadow-[inset_0_0_0_2px_var(--ink),0_0_0_4px_var(--riso-yellow)] disabled:cursor-not-allowed disabled:opacity-40 aria-invalid:bg-error-field aria-invalid:shadow-[inset_0_0_0_2px_var(--red)]'

// A native select looks like an input, with a dropdown arrow drawn on the right.
// The arrow icon is defined in select-chevron in index.css (the data URI contains spaces, so it cannot go in a class name).
export const selectClass = `${fieldClass} select-chevron h-11 appearance-none pr-10`
