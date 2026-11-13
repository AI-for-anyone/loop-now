import { createContext } from 'react'

// 统计页的编号分区：每张图是纸上的一个编号分区，编号由统计页按排列顺序给出（01 是关键指标）。
// 不在统计页里（没有编号）时，卡片照旧是一张白底卡片。
export const CardNumber = createContext<number | null>(null)
