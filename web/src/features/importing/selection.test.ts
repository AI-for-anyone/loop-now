import { describe, expect, it } from 'vitest'
import { makeImportConversation, makeImportFolder } from '@/test/importing'
import { counts, folderCheck, groupFolders, importBody, initialSelection, MAX_PER_FOLDER, repositoryName, withConversation, withFolder } from './selection'

describe('What the import page picks', () => {
  it('starts with every suggested folder checked, leaving out conversations already imported or that cannot be imported', () => {
    const done = makeImportConversation({ agentId: '00000000-0000-7000-8000-00000000aaaa' })
    const huge = makeImportConversation({ skipReason: 'too_large' })
    const fresh = makeImportConversation()
    const suggested = makeImportFolder({ path: '/code/app', conversations: [fresh, done, huge] })
    const other = makeImportFolder({ path: '/code/other', preChecked: false })
    const selection = initialSelection([suggested, other])
    expect([...selection.keys()]).toEqual(['/code/app'])
    expect([...selection.get('/code/app')!]).toEqual([fresh.sessionId])
    expect(folderCheck(selection, suggested)).toBe(true)
    expect(folderCheck(selection, other)).toBe(false)
  })

  it('a folder check is partial when only some of its conversations are picked, and a body lists the picked ones newest first', () => {
    const folder = makeImportFolder({ path: '/code/app', projectId: '00000000-0000-7000-8000-0000000000p1' })
    const [a, b, c] = folder.conversations
    let selection = withFolder(new Map(), folder, true)
    selection = withConversation(selection, folder, b.sessionId, false)
    expect(folderCheck(selection, folder)).toBe('indeterminate')
    expect(counts(selection)).toEqual({ folders: 1, conversations: 2 })
    expect(importBody('scan-1', [folder], selection)).toEqual({ scanId: 'scan-1', folders: [{ path: '/code/app', projectId: folder.projectId, sessionIds: [a.sessionId, c.sessionId] }] })
    // A project's own entry imports into that project.
    expect(importBody('scan-1', [folder], selection, 'project-x').folders[0].projectId).toBe('project-x')
    selection = withConversation(withConversation(selection, folder, a.sessionId, false), folder, c.sessionId, false)
    expect(selection.size).toBe(0)
    expect(importBody('scan-1', [folder], selection).folders).toEqual([])
  })

  it('one import takes at most 100 conversations of a folder: a folder check picks the newest 100 and no more can be added', () => {
    const many = Array.from({ length: MAX_PER_FOLDER + 5 }, () => makeImportConversation())
    const folder = makeImportFolder({ conversations: many })
    const selection = withFolder(new Map(), folder, true)
    expect(selection.get(folder.path)!.size).toBe(MAX_PER_FOLDER)
    expect(selection.get(folder.path)!.has(many[MAX_PER_FOLDER].sessionId)).toBe(false)
    expect(withConversation(selection, folder, many[MAX_PER_FOLDER].sessionId, true)).toBe(selection)
  })

  it('one import takes at most 4 GB of a folder: a folder check stops before it, and no more can be added', () => {
    const gb = 1024 ** 3
    const folder = makeImportFolder({ conversations: [makeImportConversation({ sizeBytes: 3 * gb }), makeImportConversation({ sizeBytes: 2 * gb }), makeImportConversation({ sizeBytes: 0.5 * gb })] })
    const [a, b, c] = folder.conversations
    const selection = withFolder(new Map(), folder, true)
    expect([...selection.get(folder.path)!]).toEqual([a.sessionId])
    expect(withConversation(selection, folder, b.sessionId, true)).toBe(selection)
    expect([...withConversation(selection, folder, c.sessionId, true).get(folder.path)!]).toEqual([a.sessionId, c.sessionId])
  })

  it('groups git folders by remote (clones of one remote share a group), newest first, and puts the rest under Other folders', () => {
    const app = makeImportFolder({ path: '/code/app', repository: { remote: 'git@github.com:me/app.git', mainCheckout: '/code/app' } })
    const notes = makeImportFolder({ path: '/tmp/notes', git: false, repository: null })
    const clone = makeImportFolder({ path: '/elsewhere/app', repository: { remote: 'https://github.com/me/app', mainCheckout: '/elsewhere/app' } })
    const local = makeImportFolder({ path: '/code/scratch', repository: { remote: null, mainCheckout: '/code/scratch' } })
    const groups = groupFolders([app, notes, clone, local])
    expect(groups.map((group) => [group.name, group.folders.map((f) => f.path)])).toEqual([
      ['me/app', ['/code/app', '/elsewhere/app']],
      ['scratch', ['/code/scratch']],
      [null, ['/tmp/notes']],
    ])
  })

  it('names a remote owner/name over ssh and https, and gives up on anything else', () => {
    expect(repositoryName('git@github.com:AI-for-anyone/loop-now.git')).toBe('AI-for-anyone/loop-now')
    expect(repositoryName('https://gitlab.com/group/sub/project.git')).toBe('sub/project')
    expect(repositoryName('ssh://git@code.example.com:2222/team/tool')).toBe('team/tool')
    expect(repositoryName('/srv/git/plain')).toBeNull()
    expect(repositoryName(null)).toBeNull()
  })
})
