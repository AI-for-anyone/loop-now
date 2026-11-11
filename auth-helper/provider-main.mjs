import { runIPC } from './ipc.mjs';
import { createProviderLogin } from './provider-login.mjs';
runIPC({ create: createProviderLogin });
