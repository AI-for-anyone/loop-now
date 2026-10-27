import { strFromU8, unzipSync } from 'fflate'
import { describe, expect, it } from 'vitest'
import { pickedFiles } from './upload'
import { skillFiles, totalSize, zipDirectory } from './zip'

function withPath(content: string, relativePath: string) {
  const file = new File([content], relativePath.split('/').at(-1)!)
  Object.defineProperty(file, 'webkitRelativePath', { value: relativePath })
  return file
}

describe('zipDirectory', () => {
  it('去掉最外层目录名，保留其余目录结构与内容', async () => {
    const files = pickedFiles([withPath('---\nname: demo\n---', 'demo/SKILL.md'), withPath('echo hi', 'demo/scripts/run.sh')])
    const blob = await zipDirectory(files)
    const entries = unzipSync(new Uint8Array(await blob.arrayBuffer()))
    expect(Object.keys(entries).sort()).toEqual(['SKILL.md', 'scripts/run.sh'])
    expect(strFromU8(entries['scripts/run.sh'])).toBe('echo hi')
    expect(totalSize(files)).toBe('---\nname: demo\n---'.length + 'echo hi'.length)
  })

  it('排除服务端会忽略的 .DS_Store 与 __MACOSX/ 条目', async () => {
    const files = pickedFiles([
      withPath('---\nname: demo\n---', 'demo/SKILL.md'),
      withPath('finder', 'demo/.DS_Store'),
      withPath('finder', 'demo/scripts/.DS_Store'),
      withPath('fork', 'demo/__MACOSX/._SKILL.md'),
      withPath('keep', 'demo/docs/__MACOSX/note.txt'),
    ])
    expect(skillFiles(files).map((f) => f.path)).toEqual(['demo/SKILL.md', 'demo/docs/__MACOSX/note.txt'])
    const entries = unzipSync(new Uint8Array(await (await zipDirectory(files)).arrayBuffer()))
    expect(Object.keys(entries).sort()).toEqual(['SKILL.md', 'docs/__MACOSX/note.txt'])
  })
})
