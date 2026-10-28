import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type PointerEvent as ReactPointerEvent,
} from "react";
import { LivePanels } from "./live-panels";
import {
  livePanelLayouts,
  readLiveLayoutPreference,
  saveLiveLayoutPreference,
  type LivePanelLayout,
  type LiveLayoutPreset,
} from "./live-layout";
import { ChangeSet, EditorState } from "@codemirror/state";
import {
  getSyncedVersion,
  receiveUpdates,
  sendableUpdates,
  type Update,
} from "@codemirror/collab";
import { type EditorView } from "@codemirror/view";
import {
  createLiveAPI,
  getLiveRoom,
  liveWebSocketURL,
  probeLiveRoomReconnect,
  type LiveRoomDocument,
  type LiveRoomSnapshot,
} from "./live-api";
import { LanguageMenu } from "./editor";
import {
  applyLiveChanges,
  diffLiveDocuments,
  livePasteExport,
  liveQueueState,
  liveSnapshotUpdates,
  nextLiveOutboundUpdate,
  normalizeLiveDocuments,
  randomLiveID,
  rebaseAfterAcceptedSnapshot,
} from "./live-collab";
import {
  classifyLiveOperationError,
  LiveOperationTracker,
  type LiveOperation,
} from "./live-operations";
import {
  captureLiveRevisionAuthority,
  isAuthorityEvent,
  snapshotCanReconcile,
  type LiveRevisionAuthority,
} from "./live-reconciliation";
import {
  decodeLiveWireEvent,
  liveJoinMessage,
  type LiveParticipant,
  type LiveWireEvent,
} from "./live-wire";
import {
  LiveConnectionController,
  type LiveConnectionState,
} from "./live-connection";
import { LiveResyncController } from "./live-resync";
import { beginLoading } from "./loading";
import { saveLiveBrowserNickname } from "./live-identity";
import {
  LiveCollaborativeEditor as WorkspaceEditor,
  livePresenceSelection,
  makeLiveEditorState,
  safeParticipantColor,
} from "./live-editor";
import {
  aggregateLiveRoomBytes,
  formatLiveBytes,
  formatLiveMebibytes,
  liveInlineRenameWidth,
  liveRemoteCursors,
  nextLiveRemoteCursorLabelExpiry,
  nextLiveTabName,
  nextLiveMenuItemIndex,
  reorderLiveTabIDs,
  type LiveTabDropPlacement,
} from "./live-room-ui";

type LiveConnection = LiveConnectionState;

export function LiveRoomWorkspace({
  initialRoom,
  clientID,
  connectionID,
  sessionID,
  preferredName,
  onStatus,
  onSaveAsPaste,
  onReauthenticate,
  authenticationRequired,
  reauthenticationGeneration,
}: {
  initialRoom: LiveRoomSnapshot;
  clientID: string;
  connectionID: string;
  sessionID: string;
  preferredName?: string;
  onStatus: (message: string) => void;
  onSaveAsPaste: (draft: {
    title: string;
    language: string;
    content: string;
  }) => void;
  onReauthenticate: () => void;
  authenticationRequired: boolean;
  reauthenticationGeneration: number;
}) {
  const api = useMemo(() => createLiveAPI(), []);
  const [documents, setDocuments] = useState(() =>
    normalizeLiveDocuments(initialRoom.documents),
  );
  const documentsRef = useRef(documents);
  const metadataRevisionRef = useRef(initialRoom.metadataRevision);
  const [activeDocumentID, setActiveDocumentID] = useState(
    initialRoom.documents[0]?.id ?? "",
  );
  const activeDocumentRef = useRef(activeDocumentID);
  const [layoutPreference, setLayoutPreference] = useState(() =>
    readLiveLayoutPreference(initialRoom.slug, initialRoom.expiresAt),
  );
  const [creatorPreset, setCreatorPreset] =
    useState<LiveLayoutPreset>("single");
  const [lastSplitLayout, setLastSplitLayout] =
    useState<LivePanelLayout>("columns");
  useEffect(() => {
    saveLiveLayoutPreference(
      initialRoom.slug,
      initialRoom.expiresAt,
      layoutPreference,
    );
  }, [initialRoom.slug, initialRoom.expiresAt, layoutPreference]);
  const documentsMenuRef = useRef<HTMLDetailsElement>(null);
  useEffect(() => {
    const dismiss = (event: globalThis.PointerEvent) => {
      const menu = documentsMenuRef.current;
      if (
        menu?.open &&
        event.target instanceof Node &&
        !menu.contains(event.target)
      )
        menu.open = false;
    };
    document.addEventListener("pointerdown", dismiss, true);
    return () => document.removeEventListener("pointerdown", dismiss, true);
  }, []);
  const [layoutReset, setLayoutReset] = useState(0);

  const [paneDocumentIDs, setPaneDocumentIDs] = useState<string[]>(() =>
    Array.from(
      { length: 4 },
      (_, index) => initialRoom.documents[index]?.id ?? "",
    ),
  );
  const newDocumentPaneRef = useRef<number | undefined>(undefined);
  const editorScrollRef = useRef(new Map<string, number>());
  const [narrowWorkspace, setNarrowWorkspace] = useState(
    () => window.matchMedia("(max-width: 799px)").matches,
  );
  useEffect(() => {
    const query = window.matchMedia("(max-width: 799px)");
    const update = () => setNarrowWorkspace(query.matches);
    query.addEventListener("change", update);
    return () => query.removeEventListener("change", update);
  }, []);
  const [participants, setParticipants] = useState<LiveParticipant[]>([]);
  const [cursorActivityNow, setCursorActivityNow] = useState(() => Date.now());
  const [localParticipantID, setLocalParticipantID] = useState("");
  const [localCanEdit, setLocalCanEdit] = useState(true);
  const [localAccessClass, setLocalAccessClass] =
    useState<LiveParticipant["accessClass"]>("collaborator");
  const [creator, setCreator] = useState(false);
  const currentPreset =
    creator || layoutPreference.followCreator
      ? creatorPreset
      : layoutPreference.preset;
  const splitEnabled = currentPreset !== "single";
  const panelLayout =
    currentPreset === "single" ? lastSplitLayout : currentPreset;
  useEffect(() => {
    if (currentPreset !== "single") setLastSplitLayout(currentPreset);
  }, [currentPreset]);

  const [roomWatchOnly, setRoomWatchOnly] = useState(false);
  const [roomFull, setRoomFull] = useState(false);
  const [connection, setConnection] = useState<LiveConnection>("connecting");
  const connectionRef = useRef<LiveConnection>("connecting");
  const [queueFull, setQueueFull] = useState(false);
  const [resyncing, setResyncing] = useState(false);
  const [metadataBusy, setMetadataBusy] = useState(false);
  const [recovery, setRecovery] = useState<
    { operationID: string; text: string; message: string } | undefined
  >();
  const [participantOpen, setParticipantOpen] = useState(false);
  const [exportOpen, setExportOpen] = useState(false);
  const [tabRenameOpen, setTabRenameOpen] = useState(false);
  const [tabRenameValue, setTabRenameValue] = useState("");
  const [participantRenameOpen, setParticipantRenameOpen] = useState(false);
  const [participantRenameValue, setParticipantRenameValue] = useState("");
  const [tabDrag, setTabDrag] = useState<
    | {
        sourceID: string;
        targetID: string;
        placement: LiveTabDropPlacement;
        layout?: Array<{ id: string; left: number; width: number }>;
        gap?: number;
      }
    | undefined
  >();
  const [pendingTabOrder, setPendingTabOrder] = useState<
    string[] | undefined
  >();
  const [roomBytes, setRoomBytes] = useState(() =>
    aggregateLiveRoomBytes(
      initialRoom.documents,
      (document) => document.content,
    ),
  );
  const connectionControllerRef = useRef<LiveConnectionController | undefined>(
    undefined,
  );
  const connectionWorkStopRef = useRef<(() => void) | undefined>(undefined);
  const resyncWorkStopRef = useRef<(() => void) | undefined>(undefined);
  const flushTimerRef = useRef<number | undefined>(undefined);
  const usageTimerRef = useRef<number | undefined>(undefined);
  const disposedRef = useRef(false);
  const fatalRef = useRef(false);
  const connectionGenerationRef = useRef(0);
  const editorStatesRef = useRef(new Map<string, EditorState>());
  const editorViewsRef = useRef(new Map<string, EditorView>());
  const syncedContentsRef = useRef(new Map<string, string>());
  const operationIDsRef = useRef(new WeakMap<object, string>());
  const operationTrackerRef = useRef(new LiveOperationTracker());
  const resyncingRef = useRef(false);
  const resyncControllerRef = useRef<
    | LiveResyncController<
        LiveRoomSnapshot,
        LiveWireEvent,
        LiveRevisionAuthority
      >
    | undefined
  >(undefined);
  const recoveryRef = useRef(false);
  const metadataOperationRef = useRef("");
  const localParticipantIDRef = useRef("");
  const localCanEditRef = useRef(true);
  const queueFullRef = useRef(false);
  const reauthenticationGenerationRef = useRef(reauthenticationGeneration);
  const statusRef = useRef(onStatus);
  const reauthenticateRef = useRef(onReauthenticate);
  const participantPopoverBoundaryRef = useRef<HTMLDivElement>(null);
  const participantTriggerRef = useRef<HTMLButtonElement>(null);
  const participantRenameInputRef = useRef<HTMLInputElement>(null);
  const participantHoverCloseRef = useRef<number | undefined>(undefined);
  const exportTriggerRef = useRef<HTMLButtonElement>(null);
  const exportMenuRef = useRef<HTMLDivElement>(null);
  const tabRenameInputRef = useRef<HTMLInputElement>(null);
  const tabStripRef = useRef<HTMLElement>(null);
  const tabPointerDragRef = useRef<
    | {
        pointerID: number;
        sourceID: string;
        startX: number;
        startY: number;
        startScrollLeft: number;
        dragging: boolean;
        targetID: string;
        placement: LiveTabDropPlacement;
        layout?: Array<{ id: string; left: number; width: number }>;
        gap?: number;
      }
    | undefined
  >(undefined);
  const suppressTabClickRef = useRef(false);
  statusRef.current = onStatus;
  reauthenticateRef.current = onReauthenticate;

  useEffect(() => {
    documentsRef.current = documents;
    activeDocumentRef.current = activeDocumentID;
  }, [documents, activeDocumentID]);

  function setConnectionState(next: LiveConnection) {
    if (recoveryRef.current && next === "connected") {
      connectionRef.current = "recovery";
      setConnection("recovery");
      return;
    }
    connectionRef.current = next;
    setConnection(next);
  }

  function send(message: Record<string, unknown>): boolean {
    return connectionControllerRef.current?.send(message) ?? false;
  }

  function setLocalParticipantAuthority(participant: LiveParticipant) {
    localCanEditRef.current = participant.canEdit;
    setLocalCanEdit(participant.canEdit);
    setLocalAccessClass(participant.accessClass);
  }

  function setQueueBlocked(blocked: boolean) {
    queueFullRef.current = blocked;
    setQueueFull(blocked);
  }

  function updateQueueState(replayed = false) {
    const queue = liveQueueState(editorStatesRef.current.values());
    if (queue.full) {
      setQueueBlocked(true);
      return;
    }
    if (replayed && queueFullRef.current) setQueueBlocked(false);
  }

  function refreshRoomUsage() {
    const total = aggregateLiveRoomBytes(
      documentsRef.current,
      (document) =>
        editorStatesRef.current.get(document.id)?.doc.toString() ??
        document.content,
    );
    setRoomBytes(total);
  }

  function scheduleRoomUsage() {
    if (usageTimerRef.current !== undefined) return;
    usageTimerRef.current = window.setTimeout(() => {
      usageTimerRef.current = undefined;
      refreshRoomUsage();
    }, 250);
  }

  function getEditorState(document: LiveRoomDocument): EditorState {
    const existing = editorStatesRef.current.get(document.id);
    if (existing) return existing;
    const created = makeLiveEditorState(document, clientID);
    editorStatesRef.current.set(document.id, created);
    syncedContentsRef.current.set(document.id, document.content);
    return created;
  }

  function updateEditorState(documentID: string, state: EditorState) {
    const previous = editorStatesRef.current.get(documentID);
    editorStatesRef.current.set(documentID, state);
    updateQueueState();
    const documentChanged =
      !previous ||
      previous.doc.toString() !== state.doc.toString() ||
      getSyncedVersion(previous) !== getSyncedVersion(state);
    if (documentChanged) {
      scheduleRoomUsage();
      scheduleFlush();
    }
  }

  function applyToEditor(documentID: string, update: Update) {
    const state = editorStatesRef.current.get(documentID);
    if (!state) return;
    const view = editorViewsRef.current.get(documentID);
    if (view) {
      view.dispatch(receiveUpdates(view.state, [update]));
      return;
    }
    editorStatesRef.current.set(
      documentID,
      state.update(receiveUpdates(state, [update])).state,
    );
  }

  function applyDocumentEvent(event: LiveWireEvent): boolean {
    if (event.type !== "changes") return true;
    let changes: ChangeSet;
    try {
      changes = ChangeSet.fromJSON(event.changes);
    } catch {
      return false;
    }
    const document = documentsRef.current.find(
      (candidate) => candidate.id === event.documentID,
    );
    if (!document) return true;
    const state = getEditorState(document);
    if (event.revision <= getSyncedVersion(state)) {
      operationTrackerRef.current.settle(
        event.operationID,
        connectionGenerationRef.current,
      );
      return true;
    }
    const expected = getSyncedVersion(state) + 1;
    if (event.revision !== expected) {
      return false;
    }
    applyToEditor(event.documentID, {
      changes,
      clientID: event.clientID,
    });
    const previous =
      syncedContentsRef.current.get(event.documentID) ?? document.content;
    syncedContentsRef.current.set(
      event.documentID,
      applyLiveChanges(previous, changes),
    );
    const nextContent = editorStatesRef.current
      .get(event.documentID)
      ?.doc.toString();
    if (nextContent !== undefined) {
      const revision = event.revision;
      const nextDocuments = documentsRef.current.map((candidate) =>
        candidate.id === event.documentID
          ? { ...candidate, content: nextContent, revision }
          : candidate,
      );
      documentsRef.current = nextDocuments;
    }
    updateQueueState(true);
    operationTrackerRef.current.settle(
      event.operationID,
      connectionGenerationRef.current,
    );
    scheduleRoomUsage();
    scheduleFlush();
    return true;
  }

  function applyMetadataEvent(
    event: Extract<
      LiveWireEvent,
      {
        type:
          | "document_created"
          | "document_updated"
          | "document_deleted"
          | "document_reordered";
      }
    >,
  ): boolean {
    if (event.metadataRevision < metadataRevisionRef.current) {
      operationTrackerRef.current.settle(
        event.operationID,
        connectionGenerationRef.current,
      );
      return true;
    }
    if (event.metadataRevision > metadataRevisionRef.current + 1) {
      return false;
    }
    let nextDocuments = documentsRef.current;
    switch (event.type) {
      case "document_created":
        if (
          !nextDocuments.some((document) => document.id === event.document.id)
        ) {
          nextDocuments = [
            ...nextDocuments,
            { ...event.document, position: nextDocuments.length },
          ];
          getEditorState(event.document);
        }
        if (
          event.operationID === metadataOperationRef.current &&
          newDocumentPaneRef.current !== undefined
        ) {
          assignPaneDocument(newDocumentPaneRef.current, event.document.id);
          newDocumentPaneRef.current = undefined;
          activeDocumentRef.current = event.document.id;
          setActiveDocumentID(event.document.id);
        }
        setPendingTabOrder(undefined);
        break;
      case "document_updated":
        nextDocuments = nextDocuments.map((document) =>
          document.id === event.documentID
            ? { ...document, name: event.name, language: event.language }
            : document,
        );
        break;
      case "document_deleted":
        editorScrollRef.current.delete(event.documentID);
        editorStatesRef.current.delete(event.documentID);
        editorViewsRef.current.delete(event.documentID);
        syncedContentsRef.current.delete(event.documentID);
        nextDocuments = nextDocuments
          .filter((document) => document.id !== event.documentID)
          .map((document, position) => ({ ...document, position }));
        setPendingTabOrder(undefined);
        break;
      case "document_reordered": {
        const position = new Map(event.order.map((id, index) => [id, index]));
        if (
          position.size !== nextDocuments.length ||
          nextDocuments.some((document) => !position.has(document.id))
        ) {
          return false;
        }
        nextDocuments = nextDocuments
          .map((document) => ({
            ...document,
            position: position.get(document.id)!,
          }))
          .sort((left, right) => left.position! - right.position!);
        setPendingTabOrder(undefined);
        break;
      }
    }
    documentsRef.current = nextDocuments;
    setDocuments(nextDocuments);
    refreshRoomUsage();
    metadataRevisionRef.current = event.metadataRevision;
    if (
      !nextDocuments.some(
        (document) => document.id === activeDocumentRef.current,
      )
    ) {
      const nextActive = nextDocuments[0]?.id ?? "";
      activeDocumentRef.current = nextActive;
      setActiveDocumentID(nextActive);
    }
    if (event.operationID === metadataOperationRef.current) {
      metadataOperationRef.current = "";
      setMetadataBusy(false);
    }
    operationTrackerRef.current.settle(
      event.operationID,
      connectionGenerationRef.current,
    );
    return true;
  }

  function enterRecovery(operation: LiveOperation, message: string) {
    recoveryRef.current = true;
    setRecovery({
      operationID: operation.id,
      text: operation.recoveryText,
      message,
    });
    setConnectionState("recovery");
    if (operation.id === metadataOperationRef.current)
      metadataOperationRef.current = "";
    setMetadataBusy(false);
    statusRef.current(message);
  }

  function recoverOperationError(
    event: Extract<LiveWireEvent, { type: "error" }>,
  ) {
    const pendingOperation = operationTrackerRef.current.get(event.operationID);
    if (
      event.code === "room_limit_reached" &&
      pendingOperation?.kind === "metadata" &&
      pendingOperation.message.type === "document_create"
    ) {
      operationTrackerRef.current.clear(pendingOperation.id);
      if (pendingOperation.id === metadataOperationRef.current) {
        metadataOperationRef.current = "";
        setMetadataBusy(false);
      }
      statusRef.current(`This room is limited to ${initialRoom.maxTabs} tabs`);
      if (!resyncingRef.current) void refreshSnapshot();
      return;
    }
    const category = classifyLiveOperationError(event.code, event.status);
    if (category === "retryable" || category === "resync") {
      if (!resyncingRef.current) void refreshSnapshot();
      return;
    }
    const operation = operationTrackerRef.current.reject(
      event.operationID,
      connectionGenerationRef.current,
    );
    if (!operation) return;
    const message =
      category === "auth"
        ? "This edit needs a new room session. Your text is preserved for recovery."
        : category === "overload"
          ? "This edit could not be accepted because the room is busy. Your text is preserved for recovery."
          : "This edit was rejected. Your text is preserved for recovery.";
    enterRecovery(operation, message);
  }

  function currentRevisionAuthority(): LiveRevisionAuthority {
    return captureLiveRevisionAuthority(
      metadataRevisionRef.current,
      documentsRef.current.map((document) => ({
        id: document.id,
        revision:
          editorStatesRef.current.get(document.id) !== undefined
            ? getSyncedVersion(editorStatesRef.current.get(document.id)!)
            : document.revision,
      })),
    );
  }

  // Snapshot contents may advance CodeMirror only from its synchronized
  // version. Local unsent updates remain in the editor and are rebased by
  // receiveUpdates; equal or older snapshots never invent an acknowledgement.
  function reconcileSnapshot(
    snapshotDocuments: LiveRoomDocument[],
    nextMetadataRevision: number,
    requestedAuthority: LiveRevisionAuthority,
    acceptedOperationIDs: string[],
  ): boolean {
    const nextDocuments = normalizeLiveDocuments(snapshotDocuments);
    const snapshotAuthority = captureLiveRevisionAuthority(
      nextMetadataRevision,
      nextDocuments.map((document) => ({
        id: document.id,
        revision: document.revision,
      })),
    );
    if (
      !snapshotCanReconcile(requestedAuthority, snapshotAuthority) ||
      !snapshotCanReconcile(currentRevisionAuthority(), snapshotAuthority)
    )
      return false;

    const nextByID = new Map(
      nextDocuments.map((document) => [document.id, document]),
    );
    const acceptedIDs = new Set(acceptedOperationIDs);
    for (const operation of operationTrackerRef.current.values()) {
      if (!acceptedIDs.has(operation.id) || operation.kind !== "metadata")
        continue;
      operationTrackerRef.current.settle(
        operation.id,
        connectionGenerationRef.current,
      );
      if (operation.id === metadataOperationRef.current) {
        metadataOperationRef.current = "";
        setMetadataBusy(false);
      }
    }
    for (const [documentID, state] of editorStatesRef.current) {
      const nextDocument = nextByID.get(documentID);
      if (!nextDocument) {
        if (sendableUpdates(state).length > 0) {
          recoveryRef.current = true;
          setRecovery({
            operationID: `deleted-${documentID}`,
            text: state.doc.toString(),
            message:
              "A tab changed while this room resynchronized. Your local text is preserved for recovery.",
          });
          setConnectionState("recovery");
        }
        editorScrollRef.current.delete(documentID);
        editorStatesRef.current.delete(documentID);
        editorViewsRef.current.delete(documentID);
        syncedContentsRef.current.delete(documentID);
        continue;
      }
      const synchronizedVersion = getSyncedVersion(state);
      if (nextDocument.revision <= synchronizedVersion) continue;
      const acceptedOperation = operationTrackerRef.current
        .values()
        .find(
          (operation) =>
            operation.kind === "document" &&
            operation.documentID === documentID &&
            acceptedIDs.has(operation.id),
        );
      if (acceptedOperation) {
        const pending = sendableUpdates(state);
        if (
          pending.length === 0 ||
          JSON.stringify(pending[0].changes.toJSON()) !==
            JSON.stringify(acceptedOperation.message.changes)
        )
          return false;
        const synced =
          syncedContentsRef.current.get(documentID) ?? state.doc.toString();
        const rebased = rebaseAfterAcceptedSnapshot(
          synced,
          nextDocument.content,
          pending,
        );
        if (!rebased) return false;
        let current = makeLiveEditorState(nextDocument, clientID);
        for (const update of rebased)
          current = current.update({ changes: update.changes }).state;
        editorStatesRef.current.set(documentID, current);
        const view = editorViewsRef.current.get(documentID);
        if (view && view.state !== current) view.setState(current);
        syncedContentsRef.current.set(documentID, nextDocument.content);
        operationTrackerRef.current.settle(
          acceptedOperation.id,
          connectionGenerationRef.current,
        );
        continue;
      }
      const synced =
        syncedContentsRef.current.get(documentID) ?? state.doc.toString();
      const changes = diffLiveDocuments(synced, nextDocument.content);
      const updates = liveSnapshotUpdates(
        changes,
        nextDocument.revision,
        synchronizedVersion,
      );
      let current = state;
      for (const update of updates) {
        current = current.update(receiveUpdates(current, [update])).state;
      }
      editorStatesRef.current.set(documentID, current);
      const view = editorViewsRef.current.get(documentID);
      if (view && view.state !== current) view.setState(current);
      syncedContentsRef.current.set(documentID, nextDocument.content);
    }

    const mergedDocuments = nextDocuments.map((document) => {
      const state = getEditorState(document);
      return {
        ...document,
        content: state.doc.toString(),
        revision: getSyncedVersion(state),
      };
    });
    documentsRef.current = mergedDocuments;
    setDocuments(mergedDocuments);
    setPendingTabOrder(undefined);
    refreshRoomUsage();
    if (nextMetadataRevision > metadataRevisionRef.current) {
      metadataRevisionRef.current = nextMetadataRevision;
    }
    if (!nextByID.has(activeDocumentRef.current)) {
      const nextActive = mergedDocuments[0]?.id ?? "";
      activeDocumentRef.current = nextActive;
      setActiveDocumentID(nextActive);
    }
    return true;
  }

  function handleEvent(event: LiveWireEvent) {
    // A snapshot is an authority boundary: durable WebSocket events received
    // while it is in flight are replayed after the snapshot, in wire order.
    if (
      isAuthorityEvent(event.type) &&
      resyncControllerRef.current?.bufferEvent(event)
    ) {
      return;
    }
    switch (event.type) {
      case "joined":
        setParticipants(event.participants);
        localParticipantIDRef.current = event.participant.id;
        setLocalParticipantID(event.participant.id);
        setLocalParticipantAuthority(event.participant);
        saveLiveBrowserNickname(
          initialRoom.slug,
          sessionID,
          event.participant.nickname,
        );
        setCreator(event.creator);
        setCreatorPreset(event.layoutPreset);
        setRoomWatchOnly(event.watchOnly);
        setRoomFull(false);
        const pendingMetadata = operationTrackerRef.current.get(
          metadataOperationRef.current,
        );
        if (!pendingMetadata) setMetadataBusy(false);
        connectionGenerationRef.current += 1;
        connectionControllerRef.current?.markJoined();
        replayPendingMetadata(connectionGenerationRef.current);
        updateQueueState(true);
        scheduleFlush();
        return;
      case "changes":
        if (!applyDocumentEvent(event)) void refreshSnapshot();
        return;
      case "document_created":
      case "document_updated":
      case "document_deleted":
      case "document_reordered":
        if (!applyMetadataEvent(event)) void refreshSnapshot();
        return;
      case "presence_joined":
      case "presence_updated":
      case "participant_renamed":
        setParticipants((current) => {
          const next = current.filter(
            (participant) => participant.id !== event.participant.id,
          );
          next.push(event.participant);
          return next;
        });
        if (
          event.type === "participant_renamed" &&
          event.participant.id === localParticipantIDRef.current
        ) {
          saveLiveBrowserNickname(
            initialRoom.slug,
            sessionID,
            event.participant.nickname,
          );
        }
        return;
      case "presence_left": {
        const remainingParticipant = event.participant;
        setParticipants((current) => {
          if (remainingParticipant) {
            return current.map((participant) =>
              participant.id === event.participantID
                ? remainingParticipant
                : participant,
            );
          }
          return current.map((participant) =>
            participant.id === event.participantID
              ? { ...participant, status: "connection_lost" }
              : participant,
          );
        });
        if (remainingParticipant?.id === localParticipantIDRef.current) {
          setLocalParticipantAuthority(remainingParticipant);
        }
        return;
      }
      case "room_layout_changed":
        setCreatorPreset(event.layoutPreset);
        return;
      case "room_mode_changed": {
        setParticipants(event.participants);
        setRoomWatchOnly(event.watchOnly);
        const local = event.participants.find(
          (participant) => participant.id === localParticipantIDRef.current,
        );
        if (local) setLocalParticipantAuthority(local);
        return;
      }
      case "participant_removed":
        setParticipants((current) =>
          current.filter(
            (participant) => participant.id !== event.participantID,
          ),
        );
        return;
      case "error":
        if (event.code === "room_expired" || event.status === "expired") {
          fatalRef.current = true;
          setConnectionState("offline");
          operationTrackerRef.current.clear();
          onStatus("Live room expired");
        } else recoverOperationError(event);
        return;
      case "status":
        if (
          event.status === "http_resync_required" &&
          !resyncControllerRef.current?.isActive()
        )
          void refreshSnapshot();
        return;
    }
  }

  function scheduleFlush() {
    if (flushTimerRef.current !== undefined) return;
    flushTimerRef.current = window.setTimeout(() => {
      flushTimerRef.current = undefined;
      flushChanges();
    }, 32);
  }

  function replayPendingMetadata(generation: number) {
    const operation = operationTrackerRef.current.get(
      metadataOperationRef.current,
    );
    if (!operation || operation.kind !== "metadata") return;
    if (!operationTrackerRef.current.shouldSend(operation.id, generation))
      return;
    if (!send(operation.message)) return;
    operationTrackerRef.current.markSent(operation.id, generation);
  }

  function flushChanges() {
    if (
      connectionRef.current !== "connected" ||
      resyncingRef.current ||
      recoveryRef.current ||
      !localCanEditRef.current
    )
      return;
    const generation = connectionGenerationRef.current;
    for (const [documentID, state] of editorStatesRef.current) {
      const pending = nextLiveOutboundUpdate(state);
      if (!pending) continue;
      const { baseVersion, update } = pending;
      const origin = update.origin;
      const operationID =
        operationIDsRef.current.get(origin) ?? randomLiveID("op-");
      operationIDsRef.current.set(origin, operationID);
      const message = {
        type: "push_changes",
        operation_id: operationID,
        client_id: clientID,
        document_id: documentID,
        base_version: baseVersion,
        changes: update.changes.toJSON(),
      };
      if (!operationTrackerRef.current.get(operationID)) {
        operationTrackerRef.current.track({
          id: operationID,
          kind: "document",
          generation,
          documentID,
          recoveryText: state.doc.toString(),
          message,
        });
      }
      if (!operationTrackerRef.current.shouldSend(operationID, generation))
        continue;
      if (!send(message)) {
        return;
      }
      operationTrackerRef.current.markSent(operationID, generation);
    }
  }

  function sendPresence(
    state: EditorState,
    documentID = activeDocumentRef.current,
  ) {
    if (!documentID || documentID !== activeDocumentRef.current) return;
    const selection = livePresenceSelection(state);
    send({
      type: "presence",
      current_tab: documentID,
      document_id: documentID,
      revision: selection.revision,
      anchor: selection.anchor,
      head: selection.head,
    });
  }

  function selectDocument(documentID: string, focusEditor = true) {
    if (!documentsRef.current.some((document) => document.id === documentID))
      return;
    if (documentID !== activeDocumentRef.current) setTabRenameOpen(false);
    if (split && !paneDocumentIDs.slice(0, paneCount).includes(documentID)) {
      const focused = paneDocumentIDs.indexOf(activeDocumentRef.current);
      assignPaneDocument(
        focused >= 0 && focused < paneCount ? focused : 0,
        documentID,
      );
    }
    activeDocumentRef.current = documentID;
    setActiveDocumentID(documentID);
    const state = editorStatesRef.current.get(documentID);
    if (state) sendPresence(state, documentID);
    if (focusEditor) editorViewsRef.current.get(documentID)?.focus();
  }

  function assignPaneDocument(index: number, documentID: string) {
    setPaneDocumentIDs((previous) => {
      const next = [...previous];
      const other = next.indexOf(documentID);
      if (other >= 0) next[other] = next[index];
      next[index] = documentID;
      return next;
    });
  }

  function sendMetadata(
    type:
      | "document_create"
      | "document_update"
      | "document_delete"
      | "document_reorder",
    fields: Record<string, unknown>,
  ): boolean {
    if (
      connection !== "connected" ||
      metadataBusy ||
      recoveryRef.current ||
      !localCanEditRef.current
    )
      return false;
    const operationID = randomLiveID("meta-");
    const message = {
      type,
      operation_id: operationID,
      client_id: clientID,
      base_version: metadataRevisionRef.current,
      ...fields,
    };
    operationTrackerRef.current.track({
      id: operationID,
      kind: "metadata",
      generation: connectionGenerationRef.current,
      recoveryText: JSON.stringify(fields),
      message,
    });
    metadataOperationRef.current = operationID;
    setMetadataBusy(true);
    if (!send(message)) {
      metadataOperationRef.current = "";
      setMetadataBusy(false);
      operationTrackerRef.current.clear(operationID);
      return false;
    } else {
      operationTrackerRef.current.markSent(
        operationID,
        connectionGenerationRef.current,
      );
    }
    return true;
  }

  function addDocument(pane?: number) {
    if (documentsRef.current.length >= initialRoom.maxTabs) {
      onStatus(`This room is limited to ${initialRoom.maxTabs} tabs`);
      return;
    }
    if (
      sendMetadata("document_create", {
        name: nextLiveTabName(documentsRef.current),
        language: "plaintext",
        content: "",
      })
    )
      newDocumentPaneRef.current = pane;
  }

  function renameDocument() {
    const document = documentsRef.current.find(
      (candidate) => candidate.id === activeDocumentRef.current,
    );
    if (!document) return;
    setTabRenameValue(document.name);
    setTabRenameOpen(true);
  }

  function cancelTabRename() {
    setTabRenameOpen(false);
    setTabRenameValue("");
  }

  function submitRename() {
    const name = tabRenameValue.trim();
    const document = documentsRef.current.find(
      (candidate) => candidate.id === activeDocumentRef.current,
    );
    if (!document) {
      cancelTabRename();
      return;
    }
    if (!name) {
      cancelTabRename();
      onStatus("Tab name cannot be empty");
      return;
    }
    if (name === document.name) {
      cancelTabRename();
      return;
    }
    sendMetadata("document_update", {
      document_id: activeDocumentRef.current,
      name,
      language: document.language,
    });
    cancelTabRename();
  }

  function updateLanguage(
    language: string,
    documentID = activeDocumentRef.current,
  ) {
    const document = documentsRef.current.find(
      (candidate) => candidate.id === documentID,
    );
    if (!document) return;
    sendMetadata("document_update", {
      document_id: documentID,
      name: document.name,
      language,
    });
  }

  function deleteDocument(documentID = activeDocumentRef.current) {
    if (documentsRef.current.length <= 1) {
      onStatus("A live room needs at least one tab");
      return;
    }
    sendMetadata("document_delete", { document_id: documentID });
  }

  function commitTabOrder(order: string[]) {
    const currentOrder = documentsRef.current.map((document) => document.id);
    if (
      order.length !== currentOrder.length ||
      order.every((documentID, index) => documentID === currentOrder[index])
    ) {
      return;
    }
    if (sendMetadata("document_reorder", { order })) {
      setPendingTabOrder(order);
    }
  }

  function moveDocument(documentID: string, offset: -1 | 1) {
    const order = documentsRef.current.map((document) => document.id);
    const index = order.indexOf(documentID);
    const targetIndex = index + offset;
    if (index < 0 || targetIndex < 0 || targetIndex >= order.length) return;
    commitTabOrder(
      reorderLiveTabIDs(
        order,
        documentID,
        order[targetIndex],
        offset < 0 ? "before" : "after",
      ),
    );
  }

  function beginTabPointerDrag(
    event: ReactPointerEvent<HTMLElement>,
    documentID: string,
  ) {
    if (
      split ||
      structuralDisabled ||
      tabRenameOpen ||
      event.button !== 0 ||
      (event.target instanceof Element &&
        event.target.closest(".live-tab-close, input"))
    ) {
      return;
    }
    tabPointerDragRef.current = {
      pointerID: event.pointerId,
      sourceID: documentID,
      startX: event.clientX,
      startY: event.clientY,
      startScrollLeft: tabStripRef.current?.scrollLeft ?? 0,
      dragging: false,
      targetID: documentID,
      placement: "after",
    };
  }

  function moveTabPointerDrag(event: ReactPointerEvent<HTMLElement>) {
    const pointer = tabPointerDragRef.current;
    if (!pointer || pointer.pointerID !== event.pointerId) return;
    const strip = tabStripRef.current;
    const shells = strip
      ? Array.from(strip.querySelectorAll<HTMLElement>(".live-tab-shell"))
      : [];
    if (
      !pointer.dragging &&
      Math.hypot(
        event.clientX - pointer.startX,
        event.clientY - pointer.startY,
      ) < 6
    ) {
      return;
    }
    if (!pointer.dragging) {
      pointer.dragging = true;
      if (strip) {
        const stripBounds = strip.getBoundingClientRect();
        pointer.layout = shells.flatMap((shell) => {
          const id = shell.dataset.documentId;
          if (!id) return [];
          const bounds = shell.getBoundingClientRect();
          return [
            {
              id,
              left: bounds.left - stripBounds.left + strip.scrollLeft,
              width: bounds.width,
            },
          ];
        });
        pointer.gap = Number.parseFloat(getComputedStyle(strip).columnGap) || 0;
        strip.setPointerCapture(event.pointerId);
      }
    }
    event.preventDefault();

    if (strip) {
      const stripBounds = strip.getBoundingClientRect();
      if (event.clientX < stripBounds.left + 32) strip.scrollLeft -= 14;
      else if (event.clientX > stripBounds.right - 32) strip.scrollLeft += 14;
    }

    const sourceShell = shells.find(
      (shell) => shell.dataset.documentId === pointer.sourceID,
    );
    if (sourceShell) {
      const scrollOffset = (strip?.scrollLeft ?? 0) - pointer.startScrollLeft;
      sourceShell.style.transform = `translate3d(${event.clientX - pointer.startX + scrollOffset}px, 0, 0)`;
    }

    let targetID = pointer.sourceID;
    let placement: LiveTabDropPlacement = "after";
    const stripBounds = strip?.getBoundingClientRect();
    const pointerContentX = stripBounds
      ? event.clientX - stripBounds.left + (strip?.scrollLeft ?? 0)
      : event.clientX;
    const targets = (pointer.layout ?? []).filter(
      (item) => item.id !== pointer.sourceID,
    );
    for (const target of targets) {
      targetID = target.id;
      placement = "after";
      if (pointerContentX < target.left + target.width / 2) {
        placement = "before";
        break;
      }
    }

    const layout = pointer.layout ?? [];
    const layoutByID = new Map(layout.map((item) => [item.id, item]));
    const shellByID = new Map(
      shells.flatMap((shell) => {
        const id = shell.dataset.documentId;
        return id ? [[id, shell] as const] : [];
      }),
    );
    const previewOrder = reorderLiveTabIDs(
      layout.map((item) => item.id),
      pointer.sourceID,
      targetID,
      placement,
    );
    let nextLeft = layout[0]?.left ?? 0;
    for (const id of previewOrder) {
      const item = layoutByID.get(id);
      if (!item) continue;
      if (id !== pointer.sourceID) {
        const shell = shellByID.get(id);
        const offset = nextLeft - item.left;
        if (Math.abs(offset) < 0.5) shell?.style.removeProperty("transform");
        else
          shell?.style.setProperty(
            "transform",
            `translate3d(${offset}px, 0, 0)`,
          );
      }
      nextLeft += item.width + (pointer.gap ?? 0);
    }
    pointer.targetID = targetID;
    pointer.placement = placement;
    setTabDrag((current) =>
      current?.sourceID === pointer.sourceID &&
      current.targetID === targetID &&
      current.placement === placement
        ? current
        : {
            sourceID: pointer.sourceID,
            targetID,
            placement,
          },
    );
  }

  function finishTabPointerDrag(
    event: ReactPointerEvent<HTMLElement>,
    commit: boolean,
  ) {
    const pointer = tabPointerDragRef.current;
    if (!pointer || pointer.pointerID !== event.pointerId) return;
    if (tabStripRef.current?.hasPointerCapture(event.pointerId)) {
      tabStripRef.current.releasePointerCapture(event.pointerId);
    }
    tabStripRef.current
      ?.querySelectorAll<HTMLElement>(".live-tab-shell")
      .forEach((shell) => shell.style.removeProperty("transform"));
    tabPointerDragRef.current = undefined;
    setTabDrag(undefined);
    if (!pointer.dragging) return;

    event.preventDefault();
    suppressTabClickRef.current = true;
    window.setTimeout(() => {
      suppressTabClickRef.current = false;
    }, 0);
    if (!commit) return;
    commitTabOrder(
      reorderLiveTabIDs(
        documentsRef.current.map((document) => document.id),
        pointer.sourceID,
        pointer.targetID,
        pointer.placement,
      ),
    );
  }

  function handleTabKeyDown(
    event: KeyboardEvent<HTMLButtonElement>,
    documentID: string,
  ) {
    if (!event.altKey || !event.shiftKey || structuralDisabled) return;
    if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
    event.preventDefault();
    event.stopPropagation();
    moveDocument(documentID, event.key === "ArrowLeft" ? -1 : 1);
  }

  async function copyRoomURL() {
    const url = new URL(
      `/live/${initialRoom.slug}`,
      window.location.origin,
    ).toString();
    try {
      await navigator.clipboard.writeText(url);
      onStatus("LiveBin room link copied");
    } catch {
      onStatus("Could not copy the room link");
    }
  }

  async function copyRecoveryText() {
    if (!recovery) return;
    try {
      await navigator.clipboard.writeText(recovery.text);
      operationTrackerRef.current.clear(recovery.operationID);
      statusRef.current("Recovery text copied");
    } catch {
      statusRef.current("Could not copy recovery text");
    }
  }

  function exportPaste(mode: "current" | "every") {
    const exportDocuments = documentsRef.current.map((document) => ({
      ...document,
      content:
        editorStatesRef.current.get(document.id)?.doc.toString() ??
        document.content,
    }));
    const draft = livePasteExport(
      exportDocuments,
      activeDocumentRef.current,
      mode,
    );
    onSaveAsPaste(draft);
    setExportOpen(false);
    exportTriggerRef.current?.focus();
  }

  function openParticipantPopover() {
    if (participantHoverCloseRef.current !== undefined) {
      window.clearTimeout(participantHoverCloseRef.current);
      participantHoverCloseRef.current = undefined;
    }
    setParticipantOpen(true);
  }

  function closeParticipantPopover(restoreFocus = true) {
    if (participantHoverCloseRef.current !== undefined) {
      window.clearTimeout(participantHoverCloseRef.current);
      participantHoverCloseRef.current = undefined;
    }
    participantRenameInputRef.current?.blur();
    setParticipantRenameOpen(false);
    setParticipantOpen(false);
    if (restoreFocus) participantTriggerRef.current?.focus();
  }

  function beginParticipantRename(nickname: string) {
    setParticipantRenameValue(nickname);
    setParticipantRenameOpen(true);
  }

  function cancelParticipantRename() {
    setParticipantRenameOpen(false);
    setParticipantRenameValue("");
  }

  function submitParticipantRename() {
    const nickname = participantRenameValue.trim();
    const currentNickname = participants.find(
      (participant) => participant.id === localParticipantID,
    )?.nickname;
    cancelParticipantRename();
    if (!nickname) {
      onStatus("Participant name cannot be empty");
      return;
    }
    if (nickname === currentNickname) return;
    send({ type: "participant_rename", name: nickname });
  }

  function scheduleParticipantPopoverClose() {
    if (participantHoverCloseRef.current !== undefined) {
      window.clearTimeout(participantHoverCloseRef.current);
    }
    participantHoverCloseRef.current = window.setTimeout(() => {
      participantHoverCloseRef.current = undefined;
      closeParticipantPopover(false);
    }, 120);
  }

  function closeExportMenu() {
    setExportOpen(false);
    exportTriggerRef.current?.focus();
  }

  function handleExportMenuKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (event.key === "Escape") {
      event.preventDefault();
      closeExportMenu();
      return;
    }
    const items = Array.from(
      event.currentTarget.querySelectorAll<HTMLButtonElement>(
        "[role=menuitem]",
      ),
    );
    const currentIndex = items.findIndex(
      (item) => item === document.activeElement,
    );
    const nextIndex = nextLiveMenuItemIndex(
      currentIndex,
      items.length,
      event.key,
    );
    if (nextIndex === undefined) return;
    event.preventDefault();
    items[nextIndex]?.focus();
  }

  useEffect(() => {
    if (!participantOpen) return;
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape") closeParticipantPopover();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [participantOpen]);

  useEffect(() => {
    if (!participantOpen) return;
    const onPointerDown = (event: PointerEvent) => {
      if (
        event.target instanceof Node &&
        !participantPopoverBoundaryRef.current?.contains(event.target)
      ) {
        closeParticipantPopover(false);
      }
    };
    document.addEventListener("pointerdown", onPointerDown);
    return () => document.removeEventListener("pointerdown", onPointerDown);
  }, [participantOpen]);

  useEffect(
    () => () => {
      if (participantHoverCloseRef.current !== undefined) {
        window.clearTimeout(participantHoverCloseRef.current);
      }
    },
    [],
  );

  useEffect(() => {
    if (!participantRenameOpen) return;
    participantRenameInputRef.current?.focus();
    participantRenameInputRef.current?.select();
  }, [participantRenameOpen]);

  useEffect(() => {
    if (!exportOpen) return;
    exportMenuRef.current
      ?.querySelector<HTMLButtonElement>("[role=menuitem]")
      ?.focus();
  }, [exportOpen]);

  useEffect(() => {
    if (!tabRenameOpen) return;
    tabRenameInputRef.current?.focus();
    tabRenameInputRef.current?.select();
  }, [tabRenameOpen]);

  function recoveryText() {
    const currentDocuments = documentsRef.current.map((document) => ({
      ...document,
      content:
        editorStatesRef.current.get(document.id)?.doc.toString() ??
        document.content,
    }));
    return livePasteExport(
      currentDocuments,
      activeDocumentRef.current,
      currentDocuments.length > 1 ? "every" : "current",
    ).content;
  }

  function stopResyncWork() {
    if (!resyncWorkStopRef.current) return;
    resyncWorkStopRef.current();
    resyncWorkStopRef.current = undefined;
  }

  function enterResyncRecovery() {
    const message =
      "Live room resynchronization could not reach a safe state. Your local text is preserved for recovery.";
    recoveryRef.current = true;
    setRecovery({
      operationID: randomLiveID("resync-recovery-"),
      text: recoveryText(),
      message,
    });
    resyncingRef.current = false;
    setResyncing(false);
    stopResyncWork();
    connectionControllerRef.current?.stop();
    setConnectionState("recovery");
    statusRef.current(message);
  }

  function refreshSnapshot() {
    resyncControllerRef.current?.start();
  }

  useEffect(() => {
    disposedRef.current = false;
    fatalRef.current = false;
    recoveryRef.current = false;
    setRecovery(undefined);
    const resyncController = new LiveResyncController<
      LiveRoomSnapshot,
      LiveWireEvent,
      LiveRevisionAuthority
    >({
      request: (signal) => getLiveRoom(api, initialRoom.slug, signal, clientID),
      captureAuthority: currentRevisionAuthority,
      reconcile: (snapshot, requestedAuthority) =>
        reconcileSnapshot(
          snapshot.documents,
          snapshot.metadataRevision,
          requestedAuthority,
          snapshot.acceptedOperationIDs,
        ),
      applyBuffered: (event) => {
        switch (event.type) {
          case "changes":
            return applyDocumentEvent(event);
          case "document_created":
          case "document_updated":
          case "document_deleted":
          case "document_reordered":
            return applyMetadataEvent(event);
          default:
            return true;
        }
      },
      onStarted: () => {
        resyncingRef.current = true;
        setResyncing(true);
        setConnectionState("reconnecting");
        if (!resyncWorkStopRef.current)
          resyncWorkStopRef.current = beginLoading();
      },
      onSucceeded: () => {
        resyncingRef.current = false;
        setResyncing(false);
        stopResyncWork();
        if (recoveryRef.current) {
          connectionControllerRef.current?.stop();
          setConnectionState("recovery");
          return;
        }
        connectionControllerRef.current?.reconnectAfterResync();
      },
      onFailed: enterResyncRecovery,
    });
    resyncControllerRef.current = resyncController;
    const controller = new LiveConnectionController({
      createSocket: (url) => new WebSocket(url),
      url: () => liveWebSocketURL(window.location.origin, initialRoom.slug),
      join: () =>
        liveJoinMessage(
          sessionID,
          connectionID,
          clientID,
          metadataRevisionRef.current,
          documentsRef.current,
          preferredName,
        ),
      onMessage: (data) => {
        try {
          const event = decodeLiveWireEvent(JSON.parse(data));
          if (!event) throw new Error("invalid live wire event");
          handleEvent(event);
        } catch {
          statusRef.current("Received an invalid live room update");
        }
      },
      onState: setConnectionState,
      onCloseStatus: (code, reason) => {
        if (code === 1013 && /expired/i.test(reason)) {
          fatalRef.current = true;
          statusRef.current("Live room expired");
        } else if (code === 1013) {
          statusRef.current("Live room is busy — reconnecting");
        }
      },
      onAuthenticationRequired: () => reauthenticateRef.current(),
      onRoomFull: () => {
        fatalRef.current = true;
        setRoomFull(true);
        setConnectionState("offline");
        statusRef.current(
          "Room is full. Ask someone to leave and reopen the link.",
        );
      },
      onRoomUnavailable: () => {
        fatalRef.current = true;
        operationTrackerRef.current.clear();
        setConnectionState("offline");
        statusRef.current("Live room is no longer available");
      },
      onReconnectExhausted: () => {
        setConnectionState("offline");
        statusRef.current(
          "Could not reconnect to the live room. Reopen the room to try again.",
        );
      },
      onWork: (active) => {
        if (active && !connectionWorkStopRef.current) {
          connectionWorkStopRef.current = beginLoading();
        } else if (!active && connectionWorkStopRef.current) {
          connectionWorkStopRef.current();
          connectionWorkStopRef.current = undefined;
        }
      },
      isOnline: () => navigator.onLine !== false,
      probeReconnect: () =>
        probeLiveRoomReconnect(api, initialRoom.slug, clientID),
    });
    connectionControllerRef.current = controller;
    controller.start();
    const onOnline = () => controller.online();
    const onOffline = () => controller.offline();
    window.addEventListener("online", onOnline);
    window.addEventListener("offline", onOffline);
    return () => {
      disposedRef.current = true;
      if (flushTimerRef.current !== undefined)
        window.clearTimeout(flushTimerRef.current);
      if (usageTimerRef.current !== undefined)
        window.clearTimeout(usageTimerRef.current);
      window.removeEventListener("online", onOnline);
      window.removeEventListener("offline", onOffline);
      resyncController.stop();
      controller.stop();
      operationTrackerRef.current.clear();
      if (resyncControllerRef.current === resyncController)
        resyncControllerRef.current = undefined;
      if (connectionControllerRef.current === controller)
        connectionControllerRef.current = undefined;
      if (connectionWorkStopRef.current) {
        connectionWorkStopRef.current();
        connectionWorkStopRef.current = undefined;
      }
      stopResyncWork();
    };
    // The room slug is the connection identity for this component.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialRoom.slug]);

  useEffect(() => {
    if (!authenticationRequired) return;
    resyncControllerRef.current?.stop();
    resyncingRef.current = false;
    setResyncing(false);
    stopResyncWork();
  }, [authenticationRequired]);

  useEffect(() => {
    if (
      authenticationRequired ||
      reauthenticationGeneration <= reauthenticationGenerationRef.current
    ) {
      return;
    }
    reauthenticationGenerationRef.current = reauthenticationGeneration;
    if (fatalRef.current || recoveryRef.current) return;
    refreshSnapshot();
  }, [authenticationRequired, reauthenticationGeneration]);

  const activeDocument =
    documents.find((document) => document.id === activeDocumentID) ??
    documents[0];
  const renderedDocuments = useMemo(() => {
    const order = pendingTabOrder;
    if (!order || order.length !== documents.length) return documents;
    const byID = new Map(documents.map((document) => [document.id, document]));
    const ordered = order.flatMap((documentID) => {
      const document = byID.get(documentID);
      return document ? [document] : [];
    });
    return ordered.length === documents.length ? ordered : documents;
  }, [documents, pendingTabOrder]);
  const paneCount = livePanelLayouts.find(
    (layout) => layout.id === panelLayout,
  )!.count;
  const split = splitEnabled && !narrowWorkspace;
  // Reconcile deleted/new documents without changing shared ordering or duplicating views.
  useEffect(() => {
    setPaneDocumentIDs((previous) => {
      const next = previous.map((id) =>
        documents.some((document) => document.id === id) ? id : "",
      );
      const available = documents.filter(
        (document) => !next.includes(document.id),
      );
      for (let index = 0; index < next.length; index += 1) {
        if (!next[index]) next[index] = available.shift()?.id ?? "";
      }
      return next.every((id, index) => id === previous[index])
        ? previous
        : next;
    });
  }, [documents]);

  function chooseLayout(preset: LiveLayoutPreset) {
    if (creator) {
      if (!send({ type: "room_layout", layout_preset: preset }))
        onStatus("Could not change room layout");
    } else {
      setLayoutPreference({ followCreator: false, preset });
    }
    cancelTabRename();
  }

  function toggleSplit() {
    chooseLayout(split ? "single" : panelLayout);
  }

  useEffect(() => {
    if (
      !split ||
      paneDocumentIDs.slice(0, paneCount).includes(activeDocumentID)
    )
      return;
    const next = paneDocumentIDs
      .slice(0, paneCount)
      .find((id) => documents.some((document) => document.id === id));
    if (documents.some((document) => document.id === activeDocumentID)) {
      assignPaneDocument(0, activeDocumentID);
    } else if (next) {
      activeDocumentRef.current = next;
      setActiveDocumentID(next);
    }
  }, [split, paneDocumentIDs, paneCount, activeDocumentID, documents]);

  const visibleDocumentIDs = split
    ? paneDocumentIDs.slice(0, paneCount).join(",")
    : (activeDocument?.id ?? "");
  useEffect(() => {
    setCursorActivityNow(Date.now());
  }, [visibleDocumentIDs, localParticipantID, participants]);
  useEffect(() => {
    const now = Date.now();
    const expiries = visibleDocumentIDs.split(",").flatMap((id) => {
      const expiry = nextLiveRemoteCursorLabelExpiry(
        participants,
        localParticipantID,
        id,
        now,
      );
      return expiry === undefined ? [] : [expiry];
    });
    if (!expiries.length) return;
    const timer = window.setTimeout(
      () => setCursorActivityNow(Date.now()),
      Math.max(0, Math.min(...expiries) - now) + 1,
    );
    return () => window.clearTimeout(timer);
  }, [visibleDocumentIDs, cursorActivityNow, localParticipantID, participants]);

  function renderEditor(document: LiveRoomDocument) {
    return (
      <WorkspaceEditor
        key={document.id}
        state={getEditorState(document)}
        language={document.language}
        readOnly={readOnly}
        remoteCursors={liveRemoteCursors(
          participants,
          localParticipantID,
          document.id,
          cursorActivityNow,
        )}
        onStateChange={(state) => {
          if (documentsRef.current.some((item) => item.id === document.id))
            updateEditorState(document.id, state);
        }}
        onSelectionChange={(state) => sendPresence(state, document.id)}
        onViewReady={(view) => {
          if (view) {
            editorViewsRef.current.set(document.id, view);
            const scrollTop = editorScrollRef.current.get(document.id) ?? 0;
            view.requestMeasure({
              read: () => scrollTop,
              write: (top) => {
                view.scrollDOM.scrollTop = top;
              },
            });
          } else {
            const previous = editorViewsRef.current.get(document.id);
            if (
              previous &&
              documentsRef.current.some((item) => item.id === document.id)
            )
              editorScrollRef.current.set(
                document.id,
                previous.scrollDOM.scrollTop,
              );
            editorViewsRef.current.delete(document.id);
          }
        }}
      />
    );
  }

  const readOnly =
    authenticationRequired ||
    queueFull ||
    !localCanEdit ||
    roomFull ||
    !!recovery;
  const structuralDisabled =
    authenticationRequired ||
    connection !== "connected" ||
    metadataBusy ||
    resyncing ||
    !localCanEdit ||
    roomFull ||
    !!recovery;
  const tabLimitReached = documents.length >= initialRoom.maxTabs;
  const addTabButton = (
    <button
      className="live-add-tab"
      type="button"
      aria-label="Add tab"
      disabled={structuralDisabled || tabLimitReached}
      title={
        tabLimitReached
          ? `This room is limited to ${initialRoom.maxTabs} tabs`
          : undefined
      }
      onClick={() => addDocument()}
    >
      +
    </button>
  );
  const tabStrip = (
    <nav
      className={`live-tab-strip${tabDrag ? " is-reordering" : ""}`}
      ref={tabStripRef}
      aria-label="Live room tabs"
      onPointerMove={moveTabPointerDrag}
      onPointerUp={(event) => finishTabPointerDrag(event, true)}
      onPointerCancel={(event) => finishTabPointerDrag(event, false)}
    >
      <span className="sr-only" id="live-tab-reorder-help">
        {split ? "" : "Drag tabs to reorder them. "}Keyboard users can press Alt
        Shift Left or Alt Shift Right.
      </span>
      {renderedDocuments.map((document) => {
        const active = document.id === activeDocument?.id;
        const dragging = tabDrag?.sourceID === document.id;
        const dropTarget =
          tabDrag?.targetID === document.id && !dragging
            ? ` is-drop-${tabDrag.placement}`
            : "";
        return (
          <div
            className={`live-tab-shell${active ? " is-active" : ""}${dragging ? " is-dragging" : ""}${dropTarget}`}
            key={document.id}
            data-document-id={document.id}
            onPointerDown={(event) => beginTabPointerDrag(event, document.id)}
          >
            {active && tabRenameOpen ? (
              <form
                className="live-tab-item live-tab-name-form"
                onSubmit={(event) => {
                  event.preventDefault();
                  submitRename();
                }}
              >
                <input
                  className="live-tab-name-input"
                  ref={tabRenameInputRef}
                  aria-label="Tab name"
                  enterKeyHint="done"
                  value={tabRenameValue}
                  maxLength={64}
                  style={{ width: liveInlineRenameWidth(tabRenameValue) }}
                  onChange={(event) => setTabRenameValue(event.target.value)}
                  onBlur={submitRename}
                  onKeyDown={(event) => {
                    if (event.key !== "Escape") return;
                    event.preventDefault();
                    event.stopPropagation();
                    cancelTabRename();
                  }}
                />
              </form>
            ) : (
              <button
                className="live-tab-item"
                type="button"
                aria-current={active ? "page" : undefined}
                aria-describedby="live-tab-reorder-help"
                aria-keyshortcuts="Alt+Shift+ArrowLeft Alt+Shift+ArrowRight"
                onKeyDown={(event) => handleTabKeyDown(event, document.id)}
                onClick={(event) => {
                  if (suppressTabClickRef.current) {
                    event.preventDefault();
                    return;
                  }
                  if (active && !structuralDisabled) renameDocument();
                  else selectDocument(document.id);
                }}
              >
                <span style={{ width: liveInlineRenameWidth(document.name) }}>
                  {document.name}
                </span>
              </button>
            )}
            <button
              className="live-tab-close"
              type="button"
              aria-label={`Delete ${document.name}`}
              title="Delete tab"
              disabled={structuralDisabled || documents.length <= 1}
              onPointerDown={(event) => {
                if (!active || !tabRenameOpen) return;
                event.preventDefault();
                cancelTabRename();
              }}
              onClick={() => deleteDocument(document.id)}
            >
              ×
            </button>
          </div>
        );
      })}
      {!split ? addTabButton : null}
    </nav>
  );
  return (
    <main className="live-room-canvas" aria-labelledby="live-room-heading">
      <h1 className="sr-only" id="live-room-heading">
        Live room
      </h1>
      <div className="live-room-topbar">
        <header className="live-room-toolbar live-room-actions-bar">
          <div className="live-room-actions">
            {!creator ? (
              <button
                type="button"
                className="live-follow-creator"
                aria-pressed={layoutPreference.followCreator}
                title={
                  layoutPreference.followCreator
                    ? "Following the creator's preset"
                    : "Follow the creator's preset"
                }
                onClick={() =>
                  setLayoutPreference({
                    followCreator: !layoutPreference.followCreator,
                    preset: currentPreset,
                  })
                }
              >
                Follow creator
              </button>
            ) : null}
            <button
              className="action-button live-split-toggle"
              type="button"
              aria-label={split ? "Close split view" : "Split view"}
              title={split ? "Close split view" : "Split view"}
              aria-pressed={split}
              onClick={toggleSplit}
              disabled={
                narrowWorkspace ||
                (creator &&
                  (connection !== "connected" ||
                    authenticationRequired ||
                    resyncing))
              }
            >
              <svg
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
                aria-hidden="true"
              >
                <rect x="4" y="4" width="16" height="16" rx="2" />
                <path d="M12 4v16" />
              </svg>
            </button>
            <button
              className="action-button"
              type="button"
              aria-label="Copy room link"
              title="Copy link"
              onClick={() => void copyRoomURL()}
            >
              <LinkIcon />
            </button>
            <div
              className="live-participant-menu"
              ref={participantPopoverBoundaryRef}
              onPointerEnter={openParticipantPopover}
              onPointerLeave={scheduleParticipantPopoverClose}
              onFocusCapture={openParticipantPopover}
              onBlur={(event) => {
                const next = event.relatedTarget;
                if (
                  next instanceof Node &&
                  participantPopoverBoundaryRef.current?.contains(next)
                ) {
                  return;
                }
                closeParticipantPopover(false);
              }}
            >
              <button
                className="live-connection-status"
                type="button"
                ref={participantTriggerRef}
                aria-controls="live-participants"
                aria-haspopup="dialog"
                aria-expanded={participantOpen}
                aria-label={`${connectionLabel(connection)}. ${participants.length} participant${participants.length === 1 ? "" : "s"}`}
                onClick={openParticipantPopover}
              >
                <span
                  className={`live-status-dot is-${connection}`}
                  aria-hidden="true"
                />
                <span className="live-participant-count">
                  {participants.length}
                </span>
              </button>
              {participantOpen ? (
                <div
                  className="live-participant-popover"
                  id="live-participants"
                  role="dialog"
                  aria-modal="false"
                  aria-label="Participants"
                >
                  {participants.length === 0 ? (
                    <p>No other participants</p>
                  ) : (
                    participants.map((participant) => (
                      <div
                        className="live-participant-row"
                        key={participant.id}
                        data-participant-id={participant.id}
                        data-connection-count={participant.connectionCount}
                        data-cursor-count={participant.cursors.length}
                      >
                        <span
                          className="live-participant-colour"
                          style={{
                            backgroundColor: safeParticipantColor(
                              participant.color,
                            ),
                          }}
                          aria-hidden="true"
                        />
                        <span>
                          {participant.id === localParticipantID ? (
                            participantRenameOpen ? (
                              <form
                                className="live-participant-name-form"
                                onSubmit={(event) => {
                                  event.preventDefault();
                                  submitParticipantRename();
                                }}
                              >
                                <input
                                  className="live-participant-name-input"
                                  ref={participantRenameInputRef}
                                  aria-label="Participant name"
                                  enterKeyHint="done"
                                  value={participantRenameValue}
                                  maxLength={64}
                                  onChange={(event) =>
                                    setParticipantRenameValue(
                                      event.target.value,
                                    )
                                  }
                                  onBlur={submitParticipantRename}
                                  onKeyDown={(event) => {
                                    if (event.key !== "Escape") return;
                                    event.preventDefault();
                                    event.stopPropagation();
                                    cancelParticipantRename();
                                  }}
                                />
                              </form>
                            ) : (
                              <button
                                className="live-participant-name-button"
                                type="button"
                                aria-label="Rename your participant name"
                                onClick={() =>
                                  beginParticipantRename(participant.nickname)
                                }
                              >
                                <PencilIcon />
                                <span className="live-participant-nickname">
                                  {participant.nickname}
                                </span>
                                <span className="live-participant-you">
                                  (You)
                                </span>
                              </button>
                            )
                          ) : (
                            <strong>{participant.nickname}</strong>
                          )}
                          <small>
                            {participant.accessClass} ·{" "}
                            {participant.status.replace("_", " ")}
                          </small>
                        </span>
                      </div>
                    ))
                  )}
                  {creator ? (
                    <div className="live-creator-controls">
                      <button
                        className="live-text-button"
                        type="button"
                        onClick={() =>
                          send({
                            type: "room_watch_only",
                            watch_only: !roomWatchOnly,
                          })
                        }
                      >
                        {roomWatchOnly ? "Unlock" : "Lock"}
                      </button>
                    </div>
                  ) : null}
                </div>
              ) : null}
            </div>
          </div>
        </header>

        {split ? (
          <div className="live-split-navigation">
            <details
              ref={documentsMenuRef}
              className="live-documents-menu"
              onBlur={(event) => {
                if (
                  event.relatedTarget instanceof Node &&
                  !event.currentTarget.contains(event.relatedTarget)
                )
                  event.currentTarget.open = false;
              }}
              onKeyDown={(event) => {
                if (event.key === "Escape") {
                  event.currentTarget.open = false;
                  event.currentTarget.querySelector("summary")?.focus();
                }
              }}
            >
              <summary>
                Documents <span>{documents.length}</span>
                <svg
                  width="12"
                  height="12"
                  viewBox="0 0 16 16"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.5"
                  aria-hidden="true"
                >
                  <path d="m4 6 4 4 4-4" />
                </svg>
              </summary>
              {tabStrip}
            </details>
            {addTabButton}
            <div
              className="live-layout-presets"
              role="group"
              aria-label="Workspace layout"
            >
              {livePanelLayouts.map((layout) => (
                <button
                  key={layout.id}
                  type="button"
                  aria-label={layout.label}
                  title={layout.label}
                  aria-pressed={panelLayout === layout.id}
                  onClick={() => chooseLayout(layout.id)}
                  disabled={
                    creator &&
                    (connection !== "connected" ||
                      authenticationRequired ||
                      resyncing)
                  }
                >
                  <span
                    className={`live-layout-icon is-${layout.id}`}
                    aria-hidden="true"
                  >
                    {Array.from({ length: layout.count }, (_, index) => (
                      <span key={index} />
                    ))}
                  </span>
                </button>
              ))}
              <button
                type="button"
                aria-label="Reset layout"
                title="Reset layout"
                onClick={() => {
                  const next = Array.from(
                    { length: 4 },
                    (_, index) => documents[index]?.id ?? "",
                  );
                  const active = activeDocumentRef.current;
                  if (!next.slice(0, paneCount).includes(active)) {
                    const index = next.indexOf(active);
                    if (index >= 0)
                      [next[0], next[index]] = [next[index], next[0]];
                    else next[0] = active;
                  }
                  setPaneDocumentIDs(next);
                  setLayoutReset((value) => value + 1);
                  cancelTabRename();
                }}
              >
                <svg
                  width="17"
                  height="17"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.5"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  aria-hidden="true"
                >
                  <path d="M3 10a9 9 0 1 1 2 8M3 4v6h6" />
                </svg>
              </button>
            </div>
          </div>
        ) : (
          tabStrip
        )}
        {!split ? (
          <div className="live-room-language-control">
            {activeDocument ? (
              <LanguageMenu
                value={activeDocument.language}
                onChange={updateLanguage}
                disabled={structuralDisabled}
              />
            ) : null}
          </div>
        ) : null}
      </div>

      <section
        className={`live-room-editor-frame${split ? " is-split" : ""}`}
        aria-label="Room editors"
      >
        <LivePanels
          layout={split ? panelLayout : "single"}
          resetVersion={layoutReset}
          documents={documents}
          paneIDs={paneDocumentIDs}
          activeDocumentID={activeDocument?.id ?? ""}
          structuralDisabled={structuralDisabled}
          canAdd={!structuralDisabled && !tabLimitReached}
          onSelect={(index, id) => {
            assignPaneDocument(index, id);
            activeDocumentRef.current = id;
            setActiveDocumentID(id);
            cancelTabRename();
            const state = editorStatesRef.current.get(id);
            if (state) sendPresence(state, id);
          }}
          onActivate={(id) => {
            if (activeDocumentRef.current !== id) selectDocument(id, false);
          }}
          onSwap={(source, target) => {
            setPaneDocumentIDs((previous) => {
              const next = [...previous];
              [next[source], next[target]] = [next[target], next[source]];
              return next;
            });
          }}
          onAdd={addDocument}
          onLanguage={(id, language) => updateLanguage(language, id)}
          renderEditor={renderEditor}
        />
      </section>

      <footer className="live-room-toolbar live-room-bottom-toolbar">
        <div className="toolbar-spacer" />
        <div className="live-room-bottom-status">
          {recovery ? (
            <span className="live-queue-warning" role="alert">
              {recovery.message}{" "}
              <button
                className="live-text-button"
                type="button"
                onClick={() => void copyRecoveryText()}
              >
                Copy recovery text
              </button>
            </span>
          ) : queueFull ? (
            <span className="live-queue-warning" role="status">
              Editor paused while queued changes replay
            </span>
          ) : roomFull ? (
            <span className="live-queue-warning" role="status">
              Room is full
            </span>
          ) : !localCanEdit ? (
            <span className="live-queue-warning" role="status">
              {localAccessClass === "collaborator" && roomWatchOnly
                ? "Room locked"
                : "View only"}
            </span>
          ) : null}
          <span className="byte-count">
            {formatLiveBytes(roomBytes)} /{" "}
            {formatLiveMebibytes(initialRoom.maxBytes)}
          </span>
        </div>
        <div className="live-export-control">
          <button
            className="primary-action"
            type="button"
            ref={exportTriggerRef}
            aria-expanded={exportOpen}
            aria-controls="live-export-menu"
            aria-haspopup="menu"
            onClick={() => setExportOpen((open) => !open)}
          >
            Save as paste
          </button>
          {exportOpen ? (
            <div
              className="live-export-menu"
              id="live-export-menu"
              ref={exportMenuRef}
              role="menu"
              aria-label="Save room as paste"
              onKeyDown={handleExportMenuKeyDown}
            >
              <button
                type="button"
                role="menuitem"
                onClick={() => exportPaste("current")}
              >
                Current tab
              </button>
              <button
                type="button"
                role="menuitem"
                onClick={() => exportPaste("every")}
              >
                Every tab
              </button>
            </div>
          ) : null}
        </div>
      </footer>
    </main>
  );
}

function PencilIcon() {
  return (
    <svg className="live-pencil-icon" viewBox="0 0 16 16" aria-hidden="true">
      <path d="m3 11.75-.5 2 2-.5 7.7-7.7-1.5-1.5L3 11.75Z" />
      <path d="m9.9 4.85 1.5 1.5" />
    </svg>
  );
}

function LinkIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71" />
      <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71" />
    </svg>
  );
}

function connectionLabel(connection: LiveConnection): string {
  switch (connection) {
    case "connected":
      return "Connected";
    case "reconnecting":
      return "Reconnecting";
    case "offline":
      return "Offline";
    case "recovery":
      return "Recovery needed";
    default:
      return "Connecting";
  }
}
