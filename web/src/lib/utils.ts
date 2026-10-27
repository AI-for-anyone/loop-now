import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

// 合并 Tailwind 类名，后出现的类覆盖先出现的冲突类。shadcn/ui 组件从这里导入。
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}
