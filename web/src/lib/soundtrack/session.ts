// The reader remounts for every chapter; what plays carries on across them.
// A session holds on to it while a reader is open, and lets it go a moment
// after the last one closes (the next chapter's reader takes it over first).

/** Session is what outlives one chapter's reader. */
export class Session<T> {
  private users = 0;
  private timer: ReturnType<typeof setTimeout> | undefined;
  value: T;

  constructor(
    private readonly fresh: () => T,
    private readonly release: (value: T) => void,
    private readonly grace = 1500,
  ) {
    this.value = fresh();
  }

  /** hold keeps the session while a reader is open; the returned function lets go. */
  hold(): () => void {
    this.users++;
    clearTimeout(this.timer);
    let held = true;
    return () => {
      if (!held) return;
      held = false;
      this.users--;
      clearTimeout(this.timer);
      this.timer = setTimeout(() => {
        if (this.users > 0) return;
        const was = this.value;
        this.value = this.fresh();
        this.release(was);
      }, this.grace);
    };
  }
}
