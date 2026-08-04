/**
 * Client-safe docs grouping. Lives outside $lib/server because the docs page
 * groups in the browser — SvelteKit (correctly) refuses to bundle anything
 * from $lib/server into client code.
 */
import type { DocGroup, DocSummary } from '$lib/types/docs'

/** Groups documents by service (alphabetical), null service → '(unassigned)'. */
export function groupDocumentsByService(documents: DocSummary[]): DocGroup[] {
  const byService = new Map<string, DocSummary[]>()
  for (const doc of documents) {
    const key = doc.service ?? '(unassigned)'
    const bucket = byService.get(key) ?? []
    bucket.push(doc)
    byService.set(key, bucket)
  }
  return [...byService.entries()]
    .sort((a, b) => a[0].localeCompare(b[0]))
    .map(([service, docs]) => ({ service, documents: docs }))
}
