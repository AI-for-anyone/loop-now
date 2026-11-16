import { createContext, useContext } from 'react'

// While temporarily unreadable, keep the drafts already mounted, but stop dependent queries and writes.
export const ProjectCredentialsAvailable = createContext(true)
export const useProjectCredentialsAvailable = () => useContext(ProjectCredentialsAvailable)
