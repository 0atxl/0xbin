type LiveOperationKind = "document" | "metadata";

export type LiveOperation = {
  id: string;
  kind: LiveOperationKind;
  generation: number;
  documentID?: string;
  recoveryText: string;
  message: Record<string, unknown>;
  sentGeneration?: number;
  terminal?: boolean;
};

/**
 * Tracks the browser-owned operation identity independently from a socket.
 * An operation may cross reconnects, but only its current record may be
 * settled; a delayed callback must not settle a replacement record.
 */
export class LiveOperationTracker {
  private readonly operations = new Map<string, LiveOperation>();

  track(operation: LiveOperation) {
    this.operations.set(operation.id, { ...operation });
  }

  get(id: string | undefined) {
    return id ? this.operations.get(id) : undefined;
  }

  values() {
    return [...this.operations.values()];
  }

  shouldSend(id: string, generation: number) {
    const operation = this.operations.get(id);
    return (
      !!operation &&
      !operation.terminal &&
      operation.sentGeneration !== generation
    );
  }

  markSent(id: string, generation: number) {
    const operation = this.operations.get(id);
    if (!operation || operation.terminal) return false;
    operation.sentGeneration = generation;
    return true;
  }

  settle(id: string | undefined, generation: number) {
    const operation = this.get(id);
    if (!operation || operation.generation > generation) return undefined;
    this.operations.delete(operation.id);
    return operation;
  }

  reject(id: string | undefined, generation: number) {
    const operation = this.get(id);
    if (!operation || operation.generation > generation) return undefined;
    operation.terminal = true;
    return operation;
  }

  clear(id?: string) {
    if (id) this.operations.delete(id);
    else this.operations.clear();
  }
}

export type LiveOperationErrorClass =
  "retryable" | "resync" | "auth" | "overload" | "validation" | "terminal";

export function classifyLiveOperationError(
  code: string,
  status?: string,
): LiveOperationErrorClass {
  switch (status) {
    case "retryable":
      return "retryable";
    case "resync_required":
      return "resync";
    case "auth_required":
      return "auth";
    case "overloaded":
      return "overload";
    case "validation":
      return "validation";
    case "terminal":
      return "terminal";
  }
  switch (code) {
    case "service_unavailable":
      return "retryable";
    case "resync_required":
      return "resync";
    case "unauthorized":
      return "auth";
    case "room_limit_reached":
    case "rate_limited":
      return "overload";
    case "invalid_request":
    case "message_too_large":
    case "name_taken":
      return "validation";
    default:
      return "terminal";
  }
}
