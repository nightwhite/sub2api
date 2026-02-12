// Shared manager for BaseDialog instances.
//
// Why:
// - Support multiple dialogs open at the same time (nested modals).
// - Ensure body scroll lock (`modal-open`) is only removed when the last dialog closes.
// - Ensure only the top-most dialog reacts to Escape.
//
// Notes:
// - This file is module-scoped, so state is shared across all BaseDialog instances.
// - We keep a simple "open order" stack; last opened dialog is considered top-most.

const dialogStack: string[] = []

function removeAll(id: string) {
  for (let i = dialogStack.length - 1; i >= 0; i--) {
    if (dialogStack[i] === id) dialogStack.splice(i, 1)
  }
}

function syncBodyScrollLock() {
  if (typeof document === 'undefined') return
  if (dialogStack.length > 0) document.body.classList.add('modal-open')
  else document.body.classList.remove('modal-open')
}

export function bringDialogToFront(id: string) {
  if (!id) return
  removeAll(id)
  dialogStack.push(id)
  syncBodyScrollLock()
}

export function removeDialog(id: string) {
  if (!id) return
  removeAll(id)
  syncBodyScrollLock()
}

export function isTopDialog(id: string): boolean {
  if (!id) return false
  return dialogStack.length > 0 && dialogStack[dialogStack.length - 1] === id
}

