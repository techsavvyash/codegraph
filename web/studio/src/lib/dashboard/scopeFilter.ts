/**
 * Pure client-side dashboard scoping (RFC-012 R9). When a service is selected
 * in the global scope, the dashboard narrows to that service's card and the
 * health flags relevant to it, while KPI totals stay graph-wide. Kept pure and
 * Svelte-free so the "which flags survive" rule is unit-testable.
 */
import type { HealthFlag, ServiceCard } from '$lib/types/dashboard'

/** Cards to render: just the selected service, or all when scope is null. */
export function filterServiceCards(
  cards: readonly ServiceCard[],
  service: string | null
): ServiceCard[] {
  if (service === null) return [...cards]
  return cards.filter((c) => c.name === service)
}

/**
 * True when `text` names `service` as a whole token, not merely as a prefix of
 * a longer service path. Services here can be path-shaped ("codegraph" vs
 * "codegraph/web/studio"), so a bare substring test would count a flag about
 * the nested service as also mentioning its prefix. We require the match to
 * not be immediately followed by a path separator or word character, and not
 * immediately preceded by one either.
 */
function textMentions(text: string, service: string): boolean {
  let from = 0
  for (;;) {
    const idx = text.indexOf(service, from)
    if (idx === -1) return false
    const before = idx === 0 ? '' : text[idx - 1]
    const after = text[idx + service.length] ?? ''
    const boundaryBefore = before === '' || !/[A-Za-z0-9/_-]/.test(before)
    const boundaryAfter = after === '' || !/[A-Za-z0-9/_-]/.test(after)
    if (boundaryBefore && boundaryAfter) return true
    from = idx + 1
  }
}

/**
 * A flag is service-specific when its text names a service that exists in the
 * graph, matched as a whole token so a flag about "codegraph/web/studio" is
 * not counted as mentioning "codegraph".
 */
function mentionedServices(flag: HealthFlag, known: readonly string[]): string[] {
  return known.filter((name) => textMentions(flag.text, name))
}

/**
 * Flags to render under a service scope: keep a flag when it mentions the
 * selected service, OR when it mentions no known service at all (aggregate /
 * graph-wide flags like "embeddings online" stay visible). Drop flags that
 * are exclusively about other services. With a null scope, everything shows.
 */
export function filterHealthFlags(
  flags: readonly HealthFlag[],
  service: string | null,
  knownServices: readonly string[]
): HealthFlag[] {
  if (service === null) return [...flags]
  return flags.filter((flag) => {
    const mentions = mentionedServices(flag, knownServices)
    if (mentions.length === 0) return true // aggregate / non-service flag
    return mentions.includes(service)
  })
}
