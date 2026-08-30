# Domain vocabulary

## Mutation Journal

The engine's `Reconciler` is the Mutation Journal: the single owner of the
Gmail mutation, server read-back, SQLite update, and undo-cache trim protocol.
Callers submit an `Intent` and render the typed `Outcome`; they do not reconcile
partial mutations themselves.
