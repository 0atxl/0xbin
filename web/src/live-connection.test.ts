import { describe, expect, it } from "vitest";
import {
  LiveConnectionController,
  type LiveReconnectProbeResult,
  type LiveSocket,
  type LiveSocketEvent,
} from "./live-connection";

class FakeSocket implements LiveSocket {
  readyState = 0;
  readonly sent: string[] = [];
  readonly closes: Array<[number | undefined, string | undefined]> = [];
  private readonly listeners = new Map<
    string,
    Array<(event: LiveSocketEvent) => void>
  >();

  send(data: string) {
    this.sent.push(data);
  }

  close(code?: number, reason?: string) {
    this.closes.push([code, reason]);
  }

  addEventListener(
    type: "open" | "message" | "close" | "error",
    listener: (event: LiveSocketEvent) => void,
  ) {
    const listeners = this.listeners.get(type) ?? [];
    listeners.push(listener);
    this.listeners.set(type, listeners);
  }

  open() {
    this.readyState = 1;
    this.emit("open", {});
  }

  message(data: string) {
    this.emit("message", { data });
  }

  closed(code: number, reason = "") {
    this.readyState = 3;
    this.emit("close", { code, reason });
  }

  private emit(type: string, event: LiveSocketEvent) {
    for (const listener of this.listeners.get(type) ?? []) listener(event);
  }
}

function controllerHarness(options?: {
  probeReconnect?: () => Promise<LiveReconnectProbeResult>;
  maxReconnectAttempts?: number;
}) {
  const sockets: FakeSocket[] = [];
  const timers = new Map<number, () => void>();
  const states: string[] = [];
  const closeStatuses: Array<[number, string]> = [];
  const work: boolean[] = [];
  let nextTimer = 1;
  let online = true;
  let authenticationRequired = 0;
  let roomFull = 0;
  let roomUnavailable = 0;
  let reconnectExhausted = 0;
  const controller = new LiveConnectionController({
    createSocket: () => {
      const socket = new FakeSocket();
      sockets.push(socket);
      return socket;
    },
    url: () => "ws://example.test/live",
    join: () => ({ type: "join" }),
    onMessage: () => {},
    onState: (state) => states.push(state),
    onCloseStatus: (code, reason) => closeStatuses.push([code, reason]),
    onAuthenticationRequired: () => authenticationRequired++,
    onRoomFull: () => roomFull++,
    onRoomUnavailable: () => roomUnavailable++,
    onReconnectExhausted: () => reconnectExhausted++,
    onWork: (active) => work.push(active),
    isOnline: () => online,
    random: () => 0.5,
    setTimer: (callback) => {
      const id = nextTimer++;
      timers.set(id, callback);
      return id;
    },
    clearTimer: (id) => timers.delete(id),
    connectTimeoutMs: 10,
    joinTimeoutMs: 10,
    retryBaseMs: 10,
    probeReconnect: options?.probeReconnect,
    maxReconnectAttempts: options?.maxReconnectAttempts,
  });
  return {
    controller,
    sockets,
    timers,
    states,
    closeStatuses,
    work,
    setOnline(value: boolean) {
      online = value;
    },
    authenticationRequired: () => authenticationRequired,
    roomFull: () => roomFull,
    roomUnavailable: () => roomUnavailable,
    reconnectExhausted: () => reconnectExhausted,
    runOnlyTimer() {
      const [id, callback] = [...timers.entries()][0] ?? [];
      if (id === undefined || !callback) throw new Error("no timer queued");
      timers.delete(id);
      callback();
    },
  };
}

describe("LiveConnectionController", () => {
  it("keeps one socket and one reconnect timer while ignoring stale callbacks", () => {
    const harness = controllerHarness();
    harness.controller.start();
    harness.controller.start();
    expect(harness.sockets).toHaveLength(1);
    const first = harness.sockets[0];
    first.closed(1006, "network lost");
    expect(harness.timers.size).toBe(1);
    harness.controller.online();
    expect(harness.timers.size).toBe(1);
    harness.runOnlyTimer();
    expect(harness.sockets).toHaveLength(2);
    const second = harness.sockets[1];
    first.open();
    expect(first.sent).toEqual([]);
    second.open();
    expect(second.sent).toEqual([JSON.stringify({ type: "join" })]);
  });

  it("reports connected only after joined and retries rejected joins safely", () => {
    const harness = controllerHarness();
    harness.controller.start();
    const socket = harness.sockets[0];
    socket.open();
    expect(harness.states).not.toContain("connected");
    socket.closed(1008, "password required");
    expect(harness.authenticationRequired()).toBe(1);
    expect(harness.states.at(-1)).toBe("offline");
    expect(harness.closeStatuses).toEqual([[1008, "password required"]]);
  });

  it("retains offline work until a joined replay and closes cleanly on unmount", () => {
    const harness = controllerHarness();
    harness.controller.start();
    const socket = harness.sockets[0];
    socket.open();
    socket.message("ignored by harness");
    expect(harness.controller.send({ type: "push_changes" })).toBe(false);
    harness.controller.markJoined();
    expect(harness.controller.send({ type: "push_changes" })).toBe(true);
    harness.setOnline(false);
    harness.controller.offline();
    expect(socket.closes.at(-1)).toEqual([1000, "browser offline"]);
    harness.setOnline(true);
    harness.controller.online();
    harness.controller.stop();
    expect(harness.sockets.at(-1)?.closes.at(-1)).toEqual([
      1000,
      "leaving live room",
    ]);
    expect(harness.timers.size).toBe(0);
  });

  it("keeps room-full sessions offline without retrying", () => {
    const full = controllerHarness();
    full.controller.start();
    full.sockets[0].closed(1013, "room limit reached");
    expect(full.roomFull()).toBe(1);
    expect(full.timers.size).toBe(0);
    expect(full.states.at(-1)).toBe("offline");
  });

  it("probes an abnormal protected reconnect before requesting a password", async () => {
    let probes = 0;
    const harness = controllerHarness({
      probeReconnect: async () => {
        probes += 1;
        return "authentication_required";
      },
    });
    harness.controller.start();
    harness.sockets[0].closed(1006);
    await Promise.resolve();

    expect(probes).toBe(1);
    expect(harness.authenticationRequired()).toBe(1);
    expect(harness.timers.size).toBe(0);
    expect(harness.states.at(-1)).toBe("offline");
  });

  it("distinguishes unavailable protected sessions", async () => {
    const unavailable = controllerHarness({
      probeReconnect: async () => "room_unavailable",
    });
    unavailable.controller.start();
    unavailable.sockets[0].closed(1006);
    await Promise.resolve();
    expect(unavailable.roomUnavailable()).toBe(1);
    expect(unavailable.timers.size).toBe(0);
  });

  it("bounds failed protected probes and reconnect attempts", async () => {
    let probes = 0;
    const harness = controllerHarness({
      probeReconnect: async () => {
        probes += 1;
        return "retry";
      },
      maxReconnectAttempts: 2,
    });
    harness.controller.start();
    harness.sockets[0].closed(1006);
    await Promise.resolve();
    expect(harness.timers.size).toBe(1);
    harness.runOnlyTimer();
    harness.sockets[1].closed(1006);
    await Promise.resolve();

    expect(probes).toBe(2);
    expect(harness.reconnectExhausted()).toBe(1);
    expect(harness.timers.size).toBe(0);
    expect(harness.states.at(-1)).toBe("offline");
  });

  it("ignores an authentication probe completed after going offline", async () => {
    let resolveProbe: ((result: LiveReconnectProbeResult) => void) | undefined;
    const harness = controllerHarness({
      probeReconnect: () =>
        new Promise((resolve) => {
          resolveProbe = resolve;
        }),
    });
    harness.controller.start();
    harness.sockets[0].closed(1006);
    harness.setOnline(false);
    harness.controller.offline();
    resolveProbe?.("authentication_required");
    await Promise.resolve();

    expect(harness.authenticationRequired()).toBe(0);
    expect(harness.timers.size).toBe(0);
    expect(harness.states.at(-1)).toBe("offline");
  });
});
