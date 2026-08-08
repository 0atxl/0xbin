import { describe, expect, it } from "vitest";
import { LiveResyncController } from "./live-resync";

type Snapshot = { revision: number };
type AuthorityEvent = { revision: number };

function deferred<Value>() {
  let resolve!: (value: Value) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<Value>((nextResolve, nextReject) => {
    resolve = nextResolve;
    reject = nextReject;
  });
  return { promise, resolve, reject };
}

function harness(options?: { bufferLimit?: number; maxAttempts?: number }) {
  const requests: Array<{
    signal: AbortSignal;
    generation: number;
    attempt: number;
    result: ReturnType<typeof deferred<Snapshot>>;
  }> = [];
  const timers = new Map<number, { callback: () => void; delay: number }>();
  const applied: number[] = [];
  const failures: string[] = [];
  let nextTimer = 1;
  let authority = 0;
  let started = 0;
  let succeeded = 0;
  let reconcile: (snapshot: Snapshot, requestedAuthority: number) => boolean = (
    snapshot,
  ) => snapshot.revision >= authority;
  let applyBuffered = (event: AuthorityEvent) => {
    if (event.revision <= authority) return true;
    if (event.revision !== authority + 1) return false;
    authority = event.revision;
    applied.push(event.revision);
    return true;
  };
  const controller = new LiveResyncController<Snapshot, AuthorityEvent, number>(
    {
      request: (signal, generation, attempt) => {
        const result = deferred<Snapshot>();
        requests.push({ signal, generation, attempt, result });
        return result.promise;
      },
      captureAuthority: () => authority,
      reconcile: (snapshot, requestedAuthority) => {
        const accepted = reconcile(snapshot, requestedAuthority);
        if (accepted) authority = snapshot.revision;
        return accepted;
      },
      applyBuffered: (event) => applyBuffered(event),
      onStarted: () => started++,
      onSucceeded: () => succeeded++,
      onFailed: (reason) => failures.push(reason),
      bufferLimit: options?.bufferLimit,
      maxAttempts: options?.maxAttempts,
      retryBaseMs: 10,
      retryMaxMs: 10,
      maxElapsedMs: 100,
      now: () => 0,
      setTimer: (callback, delay) => {
        const id = nextTimer++;
        timers.set(id, { callback, delay });
        return id;
      },
      clearTimer: (id) => timers.delete(id),
    },
  );
  return {
    controller,
    requests,
    timers,
    applied,
    failures,
    started: () => started,
    succeeded: () => succeeded,
    authority: () => authority,
    setAuthority(value: number) {
      authority = value;
    },
    setReconcile(next: typeof reconcile) {
      reconcile = next;
    },
    setApplyBuffered(next: typeof applyBuffered) {
      applyBuffered = next;
    },
    runTimer() {
      const [id, timer] = [...timers.entries()].sort(
        ([, left], [, right]) => left.delay - right.delay,
      )[0] ?? [undefined, undefined];
      if (id === undefined || !timer) throw new Error("no timer queued");
      timers.delete(id);
      timer.callback();
    },
    runDeadline() {
      const [id, timer] = [...timers.entries()].sort(
        ([, left], [, right]) => right.delay - left.delay,
      )[0] ?? [undefined, undefined];
      if (id === undefined || !timer) throw new Error("no timer queued");
      timers.delete(id);
      timer.callback();
    },
  };
}

describe("LiveResyncController", () => {
  it("ignores an older response after a newer generation succeeds", async () => {
    const state = harness();
    state.controller.start();
    state.controller.start();
    expect(state.requests[0].signal.aborted).toBe(true);
    state.requests[1].result.resolve({ revision: 2 });
    await Promise.resolve();
    state.requests[0].result.resolve({ revision: 1 });
    await Promise.resolve();

    expect(state.authority()).toBe(2);
    expect(state.succeeded()).toBe(1);
    expect(state.failures).toEqual([]);
  });

  it("replays only events newer than the accepted snapshot in wire order", async () => {
    const state = harness();
    state.controller.start();
    expect(state.controller.bufferEvent({ revision: 2 })).toBe(true);
    expect(state.controller.bufferEvent({ revision: 3 })).toBe(true);
    state.requests[0].result.resolve({ revision: 2 });
    await Promise.resolve();

    expect(state.authority()).toBe(3);
    expect(state.applied).toEqual([3]);
    expect(state.succeeded()).toBe(1);
  });

  it("treats false reconciliation as a retry instead of success", async () => {
    const state = harness();
    state.setAuthority(2);
    state.controller.start();
    state.requests[0].result.resolve({ revision: 1 });
    await Promise.resolve();
    expect(state.succeeded()).toBe(0);
    expect(
      [...state.timers.values()].filter((timer) => timer.delay === 10),
    ).toHaveLength(1);

    state.runTimer();
    state.requests[1].result.resolve({ revision: 3 });
    await Promise.resolve();
    expect(state.authority()).toBe(3);
    expect(state.succeeded()).toBe(1);
  });

  it("retries a transient request failure and terminates at its attempt bound", async () => {
    const recovered = harness({ maxAttempts: 3 });
    recovered.controller.start();
    recovered.requests[0].result.reject(new Error("temporary"));
    await Promise.resolve();
    recovered.runTimer();
    recovered.requests[1].result.resolve({ revision: 1 });
    await Promise.resolve();
    expect(recovered.succeeded()).toBe(1);

    const failed = harness({ maxAttempts: 2 });
    failed.controller.start();
    failed.requests[0].result.reject(new Error("offline"));
    await Promise.resolve();
    failed.runTimer();
    failed.requests[1].result.reject(new Error("still offline"));
    await Promise.resolve();
    expect(failed.failures).toEqual(["request_failed"]);
    expect(failed.timers.size).toBe(0);
    expect(failed.controller.isActive()).toBe(false);
  });

  it("starts a fresh request when the bounded event buffer fills", () => {
    const state = harness({ bufferLimit: 2, maxAttempts: 3 });
    state.controller.start();
    state.controller.bufferEvent({ revision: 1 });
    state.controller.bufferEvent({ revision: 2 });
    state.controller.bufferEvent({ revision: 3 });

    expect(state.requests).toHaveLength(2);
    expect(state.requests[0].signal.aborted).toBe(true);
    expect(state.controller.bufferedEventCount()).toBe(0);
    expect(state.requests[1].attempt).toBe(2);
  });

  it("requests a fresh snapshot when buffered revisions cannot converge", async () => {
    const state = harness();
    state.controller.start();
    state.controller.bufferEvent({ revision: 3 });
    state.requests[0].result.resolve({ revision: 1 });
    await Promise.resolve();
    expect(state.succeeded()).toBe(0);
    expect(
      [...state.timers.values()].filter((timer) => timer.delay === 10),
    ).toHaveLength(1);
    state.runTimer();
    expect(state.requests).toHaveLength(2);
  });

  it("cancels pending work without reporting success or failure", async () => {
    const state = harness();
    state.controller.start();
    state.controller.bufferEvent({ revision: 1 });
    state.controller.stop();
    state.requests[0].result.resolve({ revision: 1 });
    await Promise.resolve();

    expect(state.requests[0].signal.aborted).toBe(true);
    expect(state.succeeded()).toBe(0);
    expect(state.failures).toEqual([]);
    expect(state.controller.bufferedEventCount()).toBe(0);
  });

  it("aborts a snapshot that hangs beyond the total time budget", () => {
    const state = harness();
    state.controller.start();
    state.runDeadline();

    expect(state.requests[0].signal.aborted).toBe(true);
    expect(state.failures).toEqual(["request_failed"]);
    expect(state.controller.isActive()).toBe(false);
    expect(state.timers.size).toBe(0);
  });
});
