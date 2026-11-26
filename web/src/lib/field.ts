// Shared form control styles: crisp 1px hairline in mono skin; 2px ink inner border with yellow focus ring in Plate skin.
export const fieldClass =
  'w-full min-w-0 rounded-input border border-hairline-strong bg-surface-control px-3.5 text-[14.5px] text-ink outline-none placeholder:text-ink-500 focus-visible:border-ink focus-visible:shadow-focus disabled:cursor-not-allowed disabled:opacity-40 aria-invalid:border-red aria-invalid:bg-error-field [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:shadow-[inset_0_0_0_2px_var(--ink)] [[data-skin=plate]_&]:focus-visible:shadow-[inset_0_0_0_2px_var(--ink),0_0_0_4px_var(--riso-yellow)] [[data-skin=plate]_&]:aria-invalid:shadow-[inset_0_0_0_2px_var(--red)]'

// A native select looks like an input, with a dropdown arrow drawn on the right.
// The arrow icon is defined in select-chevron in the skin CSS (the data URI contains spaces, so it cannot go in a class name).
export const selectClass = `${fieldClass} select-chevron h-11 appearance-none pr-10`
