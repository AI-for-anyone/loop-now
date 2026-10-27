export const imageText = {
  name: (index: number, width: number, height: number) => `图片 ${index} · ${width}×${height}`,
  receiptMismatch: '图片上传回执与原图片不一致，草稿已保留。',
  add: '添加图片', selectFile: '选择图片文件', help: '可粘贴截图、拖入或选择 PNG、JPEG、WebP 图片。',
  preview: '待发送图片', history: '消息图片', only: '只支持 PNG、JPEG、WebP 图片，不支持其他文件格式。',
  empty: '图片为空，无法发送。', tooMany: (count: number) => `一条消息最多 ${count} 张图片。`,
  tooLarge: (bytes: number) => `单张图片不能超过 ${Math.floor(bytes / 1024 / 1024)} MB。`,
  tooTotal: (bytes: number) => `一条消息的图片合计不能超过 ${Math.floor(bytes / 1024 / 1024)} MB。`,
  remove: (index: number) => `移除图片 ${index}`, enlarge: (name: string) => `放大图片：${name}`,
  uploading: '上传中', uploadFailed: '上传失败',
  loading: '正在上传图片…', failed: '图片无法加载，请重新读取消息。', unsupported: '当前模型不支持图片输入，请更换支持看图的模型后再发送。',
  limits: (count: number, each: number, total: number) => `最多 ${count} 张，单张 ${Math.floor(each / 1024 / 1024)} MB，合计 ${Math.floor(total / 1024 / 1024)} MB。`,
}
