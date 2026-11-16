import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

// Merge Tailwind class names; later classes override earlier conflicting ones. shadcn/ui components import it from here.
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}
