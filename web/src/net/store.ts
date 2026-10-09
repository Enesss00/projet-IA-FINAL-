// Minimal observable store. Components subscribe to the whole state and
// re-render what changed (cheap: the state is small and updates are rare
// compared to animation frames, which read the store directly).

export type Listener<S> = (s: S, prev: S) => void;

export class Store<S extends object> {
  private listeners = new Set<Listener<S>>();
  constructor(private state: S) {}

  get(): S {
    return this.state;
  }

  set(patch: Partial<S> | ((s: S) => Partial<S>)): void {
    const prev = this.state;
    const p = typeof patch === "function" ? patch(prev) : patch;
    this.state = { ...prev, ...p };
    for (const l of this.listeners) {
      try {
        l(this.state, prev);
      } catch (e) {
        console.error("pitwall: listener error", e);
      }
    }
  }

  subscribe(l: Listener<S>): () => void {
    this.listeners.add(l);
    return () => this.listeners.delete(l);
  }
}
