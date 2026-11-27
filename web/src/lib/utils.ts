import { clsx, type ClassValue } from 'clsx'
import { extendTailwindMerge } from 'tailwind-merge'

// Register custom @theme radius tokens so overrides like rounded-full or rounded-none properly replace rounded-ticket / rounded-input.
const twMerge = extendTailwindMerge({
  extend: {
    theme: {
      radius: ['stamp', 'ticket', 'input', 'card', 'panel', 'bubble', 'dialog'],
    },
  },
})

// Merge Tailwind class names; later classes override earlier conflicting ones. shadcn/ui components import it from here.
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}
