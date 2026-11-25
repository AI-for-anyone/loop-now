import { agentInbox, inbox } from './inbox'
import { agents, createAgent } from './agents'
import { background } from './background'
import { app, connection } from './app'
import { chat } from './chat'
import { importing } from './importing'
import { permissions } from './permissions'
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
import { runtime } from './runtime'
import { settings } from './settings'
import { skills } from './skills'
import { sleep } from './sleep'
import { tasks } from './tasks'
import { timeline } from './timeline'
import { consoleInbox } from './console-inbox'
import { images } from './images'
import { localMode } from './local-mode'
import { shell } from './shell'
import { stats } from './stats'
import { taskInbox } from './task-inbox'

// Chinese copy for the console, split into files by feature. The shape of the
// copy table (Messages) is derived from here, and other languages must match it.
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
  importing,
  permissions,
  background,
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
  localMode,
  runtime,
} as const
