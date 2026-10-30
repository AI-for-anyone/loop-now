import { agentInbox, inbox } from './inbox'
import { agents, createAgent } from './agents'
import { app, connection } from './app'
import { chat } from './chat'
import { common } from './common'
import { config } from './config'
import { credentials } from './credentials'
import { errors } from './errors'
import { folders } from './folders'
import { identities } from './identities'
import { overview } from './overview'
import { plugins } from './plugins'
import { projects } from './projects'
import { prompts } from './prompts'
import { settings } from './settings'
import { skills } from './skills'
import { sleep } from './sleep'
import { tasks } from './tasks'
import { timeline } from './timeline'
import { consoleInbox } from './console-inbox'
import { images } from './images'
import { shell } from './shell'
import { stats } from './stats'
import { taskInbox } from './task-inbox'

// 控制台的中文文案，按功能区分文件。文案表的结构（Messages）由这里推导，其他语言须与它同形。
export const zhCN = {
  app,
  common,
  sleep,
  connection,
  errors,
  agents,
  projects,
  createAgent,
  folders,
  chat,
  prompts,
  config,
  skills,
  settings,
  inbox,
  tasks,
  agentInbox,
  identities,
  overview,
  timeline,
  plugins,
  credentials,
  consoleInbox,
  taskInbox,
  images,
  shell,
  stats,
} as const
