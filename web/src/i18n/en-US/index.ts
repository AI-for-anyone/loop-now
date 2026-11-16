import type { Messages } from '..'
import { agents, createAgent } from './agents'
import { app, connection } from './app'
import { chat } from './chat'
import { common } from './common'
import { config } from './config'
import { consoleInbox } from './console-inbox'
import { credentials } from './credentials'
import { errors } from './errors'
import { folders } from './folders'
import { identities } from './identities'
import { images } from './images'
import { localMode } from './local-mode'
import { agentInbox, inbox } from './inbox'
import { overview } from './overview'
import { plugins } from './plugins'
import { projects } from './projects'
import { prompts } from './prompts'
import { runtime } from './runtime'
import { settings } from './settings'
import { shell } from './shell'
import { skills } from './skills'
import { sleep } from './sleep'
import { stats } from './stats'
import { taskInbox } from './task-inbox'
import { tasks } from './tasks'
import { timeline } from './timeline'

// English copy for the console, split into files by feature; same structure as the Chinese copy table (Messages).
export const enUS: Messages = {
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
  localMode,
  runtime,
}
