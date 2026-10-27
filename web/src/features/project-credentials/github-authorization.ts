export const githubLocatorKey = (projectId: string, accountId?: string) => `cliAuth.${projectId}.gh.${accountId ?? 'new'}`
