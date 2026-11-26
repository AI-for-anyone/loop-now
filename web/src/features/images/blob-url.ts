// Turn an image Blob into a URL usable by <img src>: object URL in browsers (low memory, supports large files up to the preview limit)
// with a FileReader data-URL fallback for environments without URL.createObjectURL (such as jsdom).
export function blobToImageUrl(blob: Blob): Promise<{ url: string; revoke: () => void }> {
  if (typeof URL.createObjectURL === 'function') {
    const url = URL.createObjectURL(blob)
    return Promise.resolve({
      url,
      revoke: () => {
        if (typeof URL.revokeObjectURL === 'function') URL.revokeObjectURL(url)
      },
    })
  }
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      if (typeof reader.result === 'string') resolve({ url: reader.result, revoke: () => {} })
      else reject(new Error('Could not read image blob'))
    }
    reader.onerror = () => reject(reader.error ?? new Error('Could not read image blob'))
    reader.readAsDataURL(blob)
  })
}
