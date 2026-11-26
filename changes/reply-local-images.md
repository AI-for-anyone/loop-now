## English

### Pictures an Agent names by path show in its reply

- When an Agent's reply names a picture in its own folder (png, jpg, gif, webp, svg), the picture is shown as a thumbnail: a Markdown image with a relative, absolute or `file://` path, and an absolute path written in code or as plain text (the text stays; the thumbnails follow the paragraph or list item, several in one row). A click opens it large; in a regular conversation the path is a link that opens the file in the Agent's Folder tab.
- This works in task node conversations too, for the task's copy of the Agent's folder, while the task runs; after the task ends and its copy is reclaimed, only the text (or the image's alt text) is shown.
- Only files inside the Agent's own folder are shown, read through the folder's content endpoint with the same checks as the Folder tab; a path outside it, a missing file or one above the preview limit stays plain text. SVG is shown as a picture and never run: a script inside it does nothing.
- The Folder tab can open a given file (`?path=`) and previews SVG files.
- The loopctl guide tells Agents to show a picture by writing a Markdown image of a file in their own folder.
- API: `GET /agents/{agentId}/folder` and `GET /tasks/{taskId}/nodes/{nodeRunId}/folder` (`FolderLocation`: `path`, `realPath`), `GET /tasks/{taskId}/nodes/{nodeRunId}/folder/content`.

## 中文

### Agent 在回复里写出的图片路径，直接显示成图

- Agent 的回复里提到自己文件夹里的图片（png、jpg、gif、webp、svg）时，直接显示成小图：用相对路径、绝对路径或 `file://` 写的 Markdown 图片，以及写在代码里或正文里的绝对路径（文字保留，小图跟在那一段或那一项后面，几张排成一行）。点一下看大图；普通对话里，路径变成链接，点开就在这个 Agent 的“文件夹”标签页里定位到这个文件。
- 任务节点的对话里也一样，读的是任务给 Agent 复制的那份文件夹，任务运行期间有效；任务结束、副本回收后，只显示文字（或图片的说明文字）。
- 只显示 Agent 自己文件夹里的文件，通过文件夹的读取接口读，检查和“文件夹”标签页相同；文件夹外的路径、不存在的文件、超过预览上限的文件，都只显示文字。SVG 当作图片显示，不会执行：里面的脚本不起作用。
- “文件夹”标签页可以直接打开指定文件（`?path=`），也能预览 SVG。
- loopctl 指南告诉 Agent：想在回复里给人看图，就写一个指向自己文件夹里文件的 Markdown 图片。
- 接口：`GET /agents/{agentId}/folder` 和 `GET /tasks/{taskId}/nodes/{nodeRunId}/folder`（`FolderLocation`：`path`、`realPath`），`GET /tasks/{taskId}/nodes/{nodeRunId}/folder/content`。

## MAIN.md

- Pictures an Agent names by path show in its reply (2026-11-26; looking at loop-now-publicity's reply that listed `preview-2x2.png` and four SVG files by absolute path, the user asked “这种图片可以渲染出来吗” ("Can pictures like these be rendered?")). Loop Now task #15 (server and web implemented, loop-now-main verified), branch `task/reply-local-images`, built on task #10.
  - Server: `GET /agents/{agentId}/folder` and `GET /tasks/{taskId}/nodes/{nodeRunId}/folder` answer `FolderLocation {path, realPath}` (the folder as the Agent's prompt names it, and with symlinks resolved, the form a CLI prints); `GET /tasks/{taskId}/nodes/{nodeRunId}/folder/content` reads a task instance's copy read-only (`folders.Folder.Existing`: a read never recreates a reclaimed folder; 404 after the task ended). All console-only. The loopctl guide's "Context folders" says how to show a picture.
  - Console (`web/src/features/chat/local-images.ts`, `rehype-local-images.ts`): Markdown images, `file://` links, inline-code and bare absolute paths that resolve inside the folder (either root) and name an image become task #10's thumbnails after their paragraph or list item; bytes are fetched from the folder content endpoint with the preview limit and shown through a Blob URL typed by extension, so SVG renders as `<img>` and never runs; "Open original" links the content endpoint, never the blob. In regular chats the path links to the Folder tab (`?path=` selects the file); task node conversations have no Folder tab, so no link there.
  - Verified by loop-now-main: Go tests of the touched packages and `./api/...`, web six checks (1890 tests), check-english; a temporary instance with the real `claude`: an Agent wrote two PNGs and two SVGs (one with a `<script>`) and replied with a relative Markdown image, inline-code paths, a bare path, a path outside its folder and a missing file; thumbnails for the four files in their own folder only (no request for the outside path), the SVG script did not run, the preview and the Folder-tab link worked, no horizontal overflow at 1440 and 390 in zh and en. A real task step's picture showed while the task waited and fell back to its alt text after the task completed; a `../` path on the node endpoint answered 422.
  - Known: a reply whose last line is followed by `---` turns that line into a heading (Markdown's rule, seen in the test reply). The missing file's path is still a Folder-tab link (it opens the nearest folder).
