import { createContext } from 'react'

// Numbered sections of the stats page: each chart is a numbered section on the sheet, numbered by the stats page in display
// order (01 is the key metrics).
// Outside the stats page (no number), the card is still a white card as before.
export const CardNumber = createContext<number | null>(null)
