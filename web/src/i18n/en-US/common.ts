import type { Messages } from '..'
import { formatBytes, formatTime } from './format'

export const common = {
  save: 'Save',
  saving: 'Saving…',
  cancel: 'Cancel',
  confirm: 'Confirm',
  delete: 'Delete',
  remove: 'Remove',
  edit: 'Edit',
  add: 'Add',
  close: 'Close',
  dialogKinds: { create: 'New', danger: 'Danger', confirm: 'Confirm', edit: 'Edit', view: 'View', token: 'Token', choose: 'Choose', install: 'Install', update: 'Update', action: 'Action', pull: 'Pull' },
  irreversible: 'Irreversible',
  retry: 'Retry',
  reload: 'Reload',
  loading: 'Loading…',
  copy: 'Copy',
  copied: 'Copied',
  unknown: (value: string) => `Unknown (${value})`,
  bytes: (size: number) => formatBytes(size),
  labelledValue: (label: string, value: string) => `${label}: ${value}`,
  updatedAt: (time: string) => `Updated ${formatTime(time)}`,
  time: (time: string) => formatTime(time),
  unsavedLeave: 'You have unsaved changes. Leave anyway? They will be lost.',
  unsavedTitle: 'Leave this page?',
  leave: 'Leave',
  saved: 'Saved',
  unsaved: 'Unsaved',
  required: 'Required',
  upload: 'Upload',
  uploading: 'Uploading…',
  // Next to a path input: opens the system's own dialog for a folder or file (the desktop window's, or in a browser the one Loop Now's
  // server shows on its computer).
  choosePath: 'Choose…',
  // In a browser, while the server's dialog is open: it opens in front, but on macOS usually without the keyboard.
  choosingPath: 'Choosing…',
  choosingPathHint: 'The dialog is open on this computer. If keys do nothing, click it once.',
} satisfies Messages['common']
