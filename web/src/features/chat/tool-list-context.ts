import { createContext, useContext } from 'react'

// Whether ToolList rendered regular tool groups ahead of its `after` items in the default mono skin,
// so trailing background tool groups know whether to prefix a middle-dot separator.
export const ToolListLeadingContext = createContext(false)

export const useToolListLeading = () => useContext(ToolListLeadingContext)
