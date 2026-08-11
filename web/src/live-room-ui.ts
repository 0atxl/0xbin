import type { LiveRoomDocument } from "./live-api";
import type { LiveParticipant } from "./live-wire";

export type LiveRemoteCursor = {
  id: string;
  nickname: string;
  color: string;
  anchor: number;
  head: number;
  active: boolean;
};

export type LiveTabDropPlacement = "before" | "after";

export function aggregateLiveRoomBytes(
  documents: LiveRoomDocument[],
  contentForDocument: (document: LiveRoomDocument) => string,
): number {
  return documents.reduce(
    (total, document) =>
      total + new TextEncoder().encode(contentForDocument(document)).length,
    0,
  );
}

export function nextLiveTabName(
  documents: Array<Pick<LiveRoomDocument, "name">>,
): string {
  let highestIndex = 0;
  for (const document of documents) {
    const match = /^tab-?(\d+)$/i.exec(document.name.trim());
    if (match) highestIndex = Math.max(highestIndex, Number(match[1]));
  }
  return `tab${Math.max(documents.length + 1, highestIndex + 1)}`;
}

export function reorderLiveTabIDs(
  documentIDs: string[],
  sourceID: string,
  targetID: string,
  placement: LiveTabDropPlacement,
): string[] {
  if (sourceID === targetID) return [...documentIDs];
  const sourceIndex = documentIDs.indexOf(sourceID);
  const targetIndex = documentIDs.indexOf(targetID);
  if (sourceIndex < 0 || targetIndex < 0) return [...documentIDs];

  const order = documentIDs.filter((documentID) => documentID !== sourceID);
  const nextTargetIndex = order.indexOf(targetID);
  order.splice(nextTargetIndex + (placement === "after" ? 1 : 0), 0, sourceID);
  return order;
}

export function formatLiveRoomLifetime(seconds: number): string {
  if (seconds > 0 && seconds % 86_400 === 0) return `${seconds / 86_400}d`;
  if (seconds > 0 && seconds % 3_600 === 0) return `${seconds / 3_600}h`;
  if (seconds > 0 && seconds % 60 === 0) return `${seconds / 60}m`;
  return `${seconds}s`;
}

export function liveRemoteCursors(
  participants: LiveParticipant[],
  localParticipantID: string,
  activeDocumentID: string,
  syncedRevision: number,
  now: number,
): LiveRemoteCursor[] {
  return participants
    .filter(
      (participant) =>
        participant.id !== localParticipantID &&
        participant.status === "connected",
    )
    .flatMap((participant) => {
      const cursors =
        participant.cursors.length > 0
          ? participant.cursors
          : participant.cursor && participant.currentTab === activeDocumentID
            ? [
                {
                  connectionID: participant.id,
                  ...participant.cursor,
                },
              ]
            : [];
      return cursors
        .filter(
          (cursor) =>
            cursor.documentID === activeDocumentID &&
            cursor.revision <= syncedRevision,
        )
        .map((cursor) => ({
          id: `${participant.id}:${cursor.connectionID}`,
          nickname: participant.nickname,
          color: participant.color,
          anchor: cursor.anchor,
          head: cursor.head,
          active: now - Date.parse(participant.lastSeenAt) < 5_000,
        }));
    });
}

export function nextLiveMenuItemIndex(
  currentIndex: number,
  itemCount: number,
  key: string,
): number | undefined {
  if (itemCount === 0) return undefined;
  if (key === "Home") return 0;
  if (key === "End") return itemCount - 1;
  if (key === "ArrowDown") return (currentIndex + 1 + itemCount) % itemCount;
  if (key === "ArrowUp") return (currentIndex - 1 + itemCount) % itemCount;
  return undefined;
}
