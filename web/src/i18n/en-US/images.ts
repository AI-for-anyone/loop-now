import type { Messages } from '..'

const mb = (bytes: number) => Math.floor(bytes / 1024 / 1024)

export const images = {
  name: (index: number, width: number, height: number) => `Image ${index} · ${width}×${height}`,
  receiptMismatch: "The upload receipt doesn't match the original image; your draft was kept.",
  add: 'Add image',
  selectFile: 'Choose an image file',
  help: 'Paste a screenshot, drag one in, or choose a PNG, JPEG, or WebP image.',
  preview: 'Images to send',
  history: 'Message images',
  only: 'Only PNG, JPEG, and WebP images are supported.',
  empty: "The image is empty and can't be sent.",
  tooMany: (count: number) => `At most ${count} images per message.`,
  tooLarge: (bytes: number) => `Each image can be at most ${mb(bytes)} MB.`,
  tooTotal: (bytes: number) => `Images in one message can total at most ${mb(bytes)} MB.`,
  remove: (index: number) => `Remove image ${index}`,
  enlarge: (name: string) => `Enlarge image: ${name}`,
  uploading: 'Uploading',
  uploadFailed: 'Upload failed',
  loading: 'Uploading images…',
  failed: "The image couldn't load. Reload the messages.",
  unsupported: "The current model doesn't accept images. Switch to a model that supports images, then send.",
  limits: (count: number, each: number, total: number) => `Up to ${count} images, ${mb(each)} MB each, ${mb(total)} MB total.`,
} satisfies Messages['images']
