export type LiveResyncReconcileResult = boolean | "terminal";

export type LiveResyncOptions<Snapshot, Event, Authority> = {
  request: (
    signal: AbortSignal,
    generation: number,
    attempt: number,
  ) => Promise<Snapshot>;
  captureAuthority: () => Authority;
  reconcile: (
    snapshot: Snapshot,
    requestedAuthority: Authority,
  ) => LiveResyncReconcileResult;
  applyBuffered: (event: Event) => boolean;
  onStarted: () => void;
  onSucceeded: () => void;
  onFailed: (reason: "reconciliation_failed" | "request_failed") => void;
  bufferLimit?: number;
  maxAttempts?: number;
  maxElapsedMs?: number;
  retryBaseMs?: number;
  retryMaxMs?: number;
  now?: () => number;
  setTimer?: (callback: () => void, delay: number) => number;
  clearTimer?: (timer: number) => void;
};

export const liveResyncBufferLimit = 128;

/**
 * Owns the bounded HTTP authority boundary for one live room. Each new
 * attempt starts after its buffer is cleared, so every event received before
 * the request is covered by that snapshot and every later event is replayed
 * once in wire order.
 */
export class LiveResyncController<Snapshot, Event, Authority> {
  private readonly bufferLimit: number;
  private readonly maxAttempts: number;
  private readonly maxElapsedMs: number;
  private readonly retryBaseMs: number;
  private readonly retryMaxMs: number;
  private readonly now: () => number;
  private readonly setTimer: (callback: () => void, delay: number) => number;
  private readonly clearTimer: (timer: number) => void;
  private active = false;
  private attempt = 0;
  private startedAt = 0;
  private generation = 0;
  private buffer: Event[] = [];
  private requestController: AbortController | undefined;
  private retryTimer: number | undefined;
  private deadlineTimer: number | undefined;

  constructor(
    private readonly options: LiveResyncOptions<Snapshot, Event, Authority>,
  ) {
    this.bufferLimit = options.bufferLimit ?? liveResyncBufferLimit;
    this.maxAttempts = options.maxAttempts ?? 4;
    this.maxElapsedMs = options.maxElapsedMs ?? 10_000;
    this.retryBaseMs = options.retryBaseMs ?? 150;
    this.retryMaxMs = options.retryMaxMs ?? 1_200;
    this.now = options.now ?? Date.now;
    this.setTimer = options.setTimer ?? window.setTimeout.bind(window);
    this.clearTimer = options.clearTimer ?? window.clearTimeout.bind(window);
  }

  isActive() {
    return this.active;
  }

  bufferedEventCount() {
    return this.buffer.length;
  }

  start() {
    if (!this.active) {
      this.active = true;
      this.attempt = 0;
      this.startedAt = this.now();
      this.deadlineTimer = this.setTimer(() => {
        this.deadlineTimer = undefined;
        if (this.active) this.fail("request_failed");
      }, this.maxElapsedMs);
      this.options.onStarted();
    }
    this.beginAttempt();
  }

  bufferEvent(event: Event): boolean {
    if (!this.active) return false;
    if (this.buffer.length < this.bufferLimit) {
      this.buffer.push(event);
      return true;
    }
    // The overflowing event was durably published before it reached the
    // client. A new request begins after that publication and therefore
    // includes it without retaining an unbounded client-side queue.
    this.beginAttempt();
    return true;
  }

  stop() {
    this.generation += 1;
    this.cancelAttempt();
    this.clearDeadline();
    this.active = false;
    this.buffer = [];
  }

  private beginAttempt() {
    if (!this.active) return;
    this.cancelAttempt();
    if (!this.hasBudget(0)) {
      this.fail("request_failed");
      return;
    }
    this.attempt += 1;
    const generation = ++this.generation;
    const requestedAuthority = this.options.captureAuthority();
    const controller = new AbortController();
    this.requestController = controller;
    this.buffer = [];
    void this.options.request(controller.signal, generation, this.attempt).then(
      (snapshot) => {
        if (!this.isCurrent(generation, controller)) return;
        this.requestController = undefined;
        const reconciled = this.options.reconcile(snapshot, requestedAuthority);
        if (reconciled === "terminal") {
          this.fail("reconciliation_failed");
          return;
        }
        if (!reconciled) {
          this.retry("reconciliation_failed");
          return;
        }
        const buffered = this.buffer;
        this.buffer = [];
        for (const event of buffered) {
          if (!this.options.applyBuffered(event)) {
            this.retry("reconciliation_failed");
            return;
          }
        }
        this.active = false;
        this.clearDeadline();
        this.options.onSucceeded();
      },
      (error: unknown) => {
        if (!this.isCurrent(generation, controller)) return;
        this.requestController = undefined;
        if (error instanceof DOMException && error.name === "AbortError")
          return;
        this.retry("request_failed");
      },
    );
  }

  private retry(reason: "reconciliation_failed" | "request_failed") {
    if (!this.active) return;
    const delay = Math.min(
      this.retryMaxMs,
      this.retryBaseMs * 2 ** Math.max(0, this.attempt - 1),
    );
    if (!this.hasBudget(delay)) {
      this.fail(reason);
      return;
    }
    this.retryTimer = this.setTimer(() => {
      this.retryTimer = undefined;
      this.beginAttempt();
    }, delay);
  }

  private hasBudget(nextDelay: number) {
    return (
      this.attempt < this.maxAttempts &&
      this.now() - this.startedAt + nextDelay <= this.maxElapsedMs
    );
  }

  private fail(reason: "reconciliation_failed" | "request_failed") {
    this.cancelAttempt();
    this.clearDeadline();
    this.active = false;
    this.buffer = [];
    this.options.onFailed(reason);
  }

  private isCurrent(generation: number, controller: AbortController) {
    return (
      this.active &&
      this.generation === generation &&
      this.requestController === controller &&
      !controller.signal.aborted
    );
  }

  private cancelAttempt() {
    this.requestController?.abort();
    this.requestController = undefined;
    if (this.retryTimer !== undefined) {
      this.clearTimer(this.retryTimer);
      this.retryTimer = undefined;
    }
  }

  private clearDeadline() {
    if (this.deadlineTimer === undefined) return;
    this.clearTimer(this.deadlineTimer);
    this.deadlineTimer = undefined;
  }
}
