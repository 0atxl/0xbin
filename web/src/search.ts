export type SearchMatch = {
  from: number;
  to: number;
};

export function findSearchMatches(
  content: string,
  query: string,
): SearchMatch[] {
  if (!query) return [];

  const lowerContent = content.toLocaleLowerCase();
  const lowerQuery = query.toLocaleLowerCase();
  const matches: SearchMatch[] = [];
  let from = lowerContent.indexOf(lowerQuery);

  while (from !== -1) {
    matches.push({ from, to: from + query.length });
    from = lowerContent.indexOf(lowerQuery, from + query.length);
  }

  return matches;
}
