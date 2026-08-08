export type LiveRevision = {
  id: string;
  revision: number;
};

export type LiveRevisionAuthority = {
  metadataRevision: number;
  documents: LiveRevision[];
};

/** Capture the authority the browser had when an HTTP request began. */
export function captureLiveRevisionAuthority(
  metadataRevision: number,
  documents: Iterable<LiveRevision>,
): LiveRevisionAuthority {
  return {
    metadataRevision,
    documents: [...documents].map((document) => ({ ...document })),
  };
}

/**
 * A snapshot may only advance authority. A missing known document is valid
 * only when metadata moved forward, because deletion is a metadata operation.
 */
export function snapshotCanReconcile(
  current: LiveRevisionAuthority,
  snapshot: LiveRevisionAuthority,
): boolean {
  if (snapshot.metadataRevision < current.metadataRevision) return false;
  const snapshotDocuments = new Map(
    snapshot.documents.map((document) => [document.id, document.revision]),
  );
  for (const document of current.documents) {
    const snapshotRevision = snapshotDocuments.get(document.id);
    if (snapshotRevision === undefined) {
      if (snapshot.metadataRevision <= current.metadataRevision) return false;
      continue;
    }
    if (snapshotRevision < document.revision) return false;
  }
  return true;
}

export function isAuthorityEvent(type: string): boolean {
  return (
    type === "changes" ||
    type === "document_created" ||
    type === "document_updated" ||
    type === "document_deleted" ||
    type === "document_reordered"
  );
}

export function isCurrentLiveSnapshot(
  requestGeneration: number,
  activeGeneration: number,
): boolean {
  return requestGeneration === activeGeneration;
}
