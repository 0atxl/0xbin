export type LiveConnectionState =
  "connecting" | "connected" | "reconnecting" | "offline" | "recovery";

export type LiveReconnectProbeResult =
  | "authorized"
  | "authentication_required"
  | "room_unavailable"
  | "removed"
  | "retry";

export type LiveSocketEvent = {
  data?: unknown;
  code?: number;
  reason?: string;
};

export type LiveSocket = {
  readonly readyState: number;
  send(data: string): void;
  close(code?: number, reason?: string): void;
  addEventListener(
    type: "open" | "message" | "close" | "error",
    listener: (event: LiveSocketEvent) => void,
  ): void;
};

export type LiveConnectionOptions = {
  createSocket: (url: string) => LiveSocket;
  url: () => string;
  join: () => Record<string, unknown>;
  onMessage: (data: string) => void;
  onState: (state: LiveConnectionState) => void;
  onCloseStatus: (code: number, reason: string) => void;
  onAuthenticationRequired: () => void;
  onRemoved?: () => void;
  onRoomFull?: () => void;
  onRoomUnavailable?: () => void;
  onReconnectExhausted?: () => void;
  onWork: (active: boolean) => void;
  isOnline: () => boolean;
  probeReconnect?: () => Promise<LiveReconnectProbeResult>;
  random?: () => number;
  setTimer?: (callback: () => void, delay: number) => number;
  clearTimer?: (timer: number) => void;
  connectTimeoutMs?: number;
  joinTimeoutMs?: number;
  retryBaseMs?: number;
  retryMaxMs?: number;
  maxReconnectAttempts?: number;
};

const socketOpen = 1;
const normalClose = 1000;
const policyViolation = 1008;
const tryAgainLater = 1013;

/**
 * Owns one live-room socket and one reconnect timer. A socket callback may
 * mutate state only while it is still the controller's current attempt.
 */
export class LiveConnectionController {
  private readonly random: () => number;
  private readonly setTimer: (callback: () => void, delay: number) => number;
  private readonly clearTimer: (timer: number) => void;
  private readonly connectTimeoutMs: number;
  private readonly joinTimeoutMs: number;
  private readonly retryBaseMs: number;
  private readonly retryMaxMs: number;
  private readonly maxReconnectAttempts: number;
  private socket: LiveSocket | undefined;
  private reconnectTimer: number | undefined;
  private connectTimer: number | undefined;
  private joinTimer: number | undefined;
  private stopped = false;
  private probing = false;
  private probeGeneration = 0;
  private joined = false;
  private attempt = 0;
  private state: LiveConnectionState = "offline";

  constructor(private readonly options: LiveConnectionOptions) {
    this.random = options.random ?? Math.random;
    this.setTimer = options.setTimer ?? window.setTimeout.bind(window);
    this.clearTimer = options.clearTimer ?? window.clearTimeout.bind(window);
    this.connectTimeoutMs = options.connectTimeoutMs ?? 10_000;
    this.joinTimeoutMs = options.joinTimeoutMs ?? 10_000;
    this.retryBaseMs = options.retryBaseMs ?? 250;
    this.retryMaxMs = options.retryMaxMs ?? 5_000;
    this.maxReconnectAttempts = options.maxReconnectAttempts ?? 8;
  }

  start() {
    this.stopped = false;
    this.connect();
  }

  stop() {
    this.stopped = true;
    this.cancelProbe();
    this.clearTimers();
    const socket = this.socket;
    this.socket = undefined;
    this.joined = false;
    if (socket) socket.close(normalClose, "leaving live room");
    this.setState("offline");
  }

  online() {
    if (
      this.stopped ||
      this.socket ||
      this.probing ||
      this.reconnectTimer !== undefined
    )
      return;
    this.attempt = 0;
    this.connect();
  }

  offline() {
    if (this.stopped) return;
    this.cancelProbe();
    this.clearReconnectTimer();
    const socket = this.socket;
    this.socket = undefined;
    this.clearAttemptTimers();
    this.joined = false;
    if (socket) socket.close(normalClose, "browser offline");
    this.setState("offline");
  }

  markJoined() {
    if (!this.socket || this.stopped) return;
    this.joined = true;
    this.attempt = 0;
    this.clearJoinTimer();
    this.setState("connected");
  }

  reconnectAfterResync() {
    if (this.stopped) return;
    const socket = this.socket;
    this.socket = undefined;
    this.clearAttemptTimers();
    this.joined = false;
    if (socket) socket.close(normalClose, "resynchronizing");
    this.scheduleReconnect(0);
  }

  send(message: Record<string, unknown>): boolean {
    const socket = this.socket;
    if (!socket || !this.joined || socket.readyState !== socketOpen)
      return false;
    socket.send(JSON.stringify(message));
    return true;
  }

  private connect() {
    if (this.stopped || this.socket || !this.options.isOnline()) {
      if (!this.stopped && !this.options.isOnline()) this.setState("offline");
      return;
    }
    this.clearReconnectTimer();
    this.joined = false;
    this.setState(this.attempt === 0 ? "connecting" : "reconnecting");
    const socket = this.options.createSocket(this.options.url());
    this.socket = socket;
    this.connectTimer = this.setTimer(() => {
      if (!this.isCurrent(socket) || this.joined) return;
      socket.close(policyViolation, "connection timed out");
    }, this.connectTimeoutMs);
    socket.addEventListener("open", () => {
      if (!this.isCurrent(socket)) return;
      this.clearConnectTimer();
      socket.send(JSON.stringify(this.options.join()));
      this.joinTimer = this.setTimer(() => {
        if (!this.isCurrent(socket) || this.joined) return;
        socket.close(policyViolation, "join timed out");
      }, this.joinTimeoutMs);
    });
    socket.addEventListener("message", (event) => {
      if (!this.isCurrent(socket) || typeof event.data !== "string") return;
      this.options.onMessage(event.data);
    });
    socket.addEventListener("close", (event) =>
      this.handleClose(socket, event),
    );
    socket.addEventListener("error", () => {
      // The close event owns retry decisions and supplies the useful status.
    });
  }

  private handleClose(socket: LiveSocket, event: LiveSocketEvent) {
    if (!this.isCurrent(socket)) return;
    this.socket = undefined;
    this.clearAttemptTimers();
    this.joined = false;
    const code = event.code ?? 1006;
    const reason = event.reason ?? "";
    this.options.onCloseStatus(code, reason);
    if (this.stopped || !this.options.isOnline()) {
      this.setState("offline");
      return;
    }
    if (code === policyViolation && /password|auth/i.test(reason)) {
      this.setState("offline");
      this.options.onAuthenticationRequired();
      return;
    }
    if (code === policyViolation && /removed|kicked/i.test(reason)) {
      this.setState("offline");
      this.options.onRemoved?.();
      return;
    }
    if (code === tryAgainLater && /room (limit|full)/i.test(reason)) {
      this.setState("offline");
      this.options.onRoomFull?.();
      return;
    }
    if (code === normalClose && reason === "leaving live room") {
      this.setState("offline");
      return;
    }
    if (code === tryAgainLater && /expired/i.test(reason)) {
      this.setState("offline");
      return;
    }
    this.attempt += 1;
    if (this.options.probeReconnect) {
      this.probeBeforeReconnect();
      return;
    }
    this.retryOrExhaust();
  }

  private probeBeforeReconnect() {
    if (this.stopped || this.probing || !this.options.isOnline()) {
      if (!this.stopped && !this.options.isOnline()) this.setState("offline");
      return;
    }
    this.probing = true;
    const generation = ++this.probeGeneration;
    this.setState("reconnecting");
    void this.options.probeReconnect!()
      .then((result) => {
        if (!this.isCurrentProbe(generation)) return;
        this.probing = false;
        switch (result) {
          case "authentication_required":
            this.setState("offline");
            this.options.onAuthenticationRequired();
            return;
          case "room_unavailable":
            this.setState("offline");
            this.options.onRoomUnavailable?.();
            return;
          case "removed":
            this.setState("offline");
            this.options.onRemoved?.();
            return;
          case "authorized":
          case "retry":
            this.retryOrExhaust();
        }
      })
      .catch(() => {
        if (!this.isCurrentProbe(generation)) return;
        this.probing = false;
        this.retryOrExhaust();
      });
  }

  private retryOrExhaust() {
    if (this.attempt >= this.maxReconnectAttempts) {
      this.setState("offline");
      this.options.onReconnectExhausted?.();
      return;
    }
    this.scheduleReconnect(this.backoffDelay());
  }

  private scheduleReconnect(delay: number) {
    if (
      this.stopped ||
      this.reconnectTimer !== undefined ||
      !this.options.isOnline()
    )
      return;
    this.setState("reconnecting");
    this.reconnectTimer = this.setTimer(() => {
      this.reconnectTimer = undefined;
      this.connect();
    }, delay);
  }

  private backoffDelay(): number {
    const exponential = Math.min(
      this.retryMaxMs,
      this.retryBaseMs * 2 ** Math.min(this.attempt - 1, 6),
    );
    return Math.round(exponential * (0.8 + this.random() * 0.4));
  }

  private isCurrent(socket: LiveSocket) {
    return !this.stopped && this.socket === socket;
  }

  private isCurrentProbe(generation: number) {
    return (
      !this.stopped &&
      this.probing &&
      this.probeGeneration === generation &&
      this.options.isOnline()
    );
  }

  private cancelProbe() {
    this.probing = false;
    this.probeGeneration += 1;
  }

  private setState(state: LiveConnectionState) {
    if (this.state === state) return;
    this.state = state;
    this.options.onState(state);
    this.options.onWork(state === "connecting" || state === "reconnecting");
  }

  private clearReconnectTimer() {
    if (this.reconnectTimer === undefined) return;
    this.clearTimer(this.reconnectTimer);
    this.reconnectTimer = undefined;
  }

  private clearConnectTimer() {
    if (this.connectTimer === undefined) return;
    this.clearTimer(this.connectTimer);
    this.connectTimer = undefined;
  }

  private clearJoinTimer() {
    if (this.joinTimer === undefined) return;
    this.clearTimer(this.joinTimer);
    this.joinTimer = undefined;
  }

  private clearAttemptTimers() {
    this.clearConnectTimer();
    this.clearJoinTimer();
  }

  private clearTimers() {
    this.clearReconnectTimer();
    this.clearAttemptTimers();
  }
}
