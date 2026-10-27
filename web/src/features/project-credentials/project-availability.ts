import { createContext, useContext } from 'react'

// 暂时不可读取时保留已挂载的草稿，但停止附属查询和写入。
export const ProjectCredentialsAvailable = createContext(true)
export const useProjectCredentialsAvailable = () => useContext(ProjectCredentialsAvailable)
