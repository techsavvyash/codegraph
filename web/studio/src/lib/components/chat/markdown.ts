/**
 * Safe markdown → HTML for chat bubbles. marked parses, DOMPurify sanitizes —
 * so a model that emits `<img onerror=…>` or a `javascript:` href cannot inject
 * script into the studio. Sanitization is the load-bearing step; never render
 * assistant text with {@html} without routing through here.
 *
 * DOMPurify needs a DOM. In the browser it binds to the ambient window; during
 * SSR (adapter-node) there is none, so `sanitize` is unavailable — there we
 * fall back to fully-escaped text (safe, just unstyled). Assistant bubbles
 * render on the client anyway; SSR only ever sees empty content.
 */
import { marked } from 'marked'
import DOMPurify from 'dompurify'

marked.setOptions({ breaks: true, gfm: true })

const SANITIZE_CONFIG = {
  ALLOWED_TAGS: [
    'p', 'br', 'strong', 'em', 'code', 'pre', 'ul', 'ol', 'li',
    'h1', 'h2', 'h3', 'h4', 'blockquote', 'hr', 'a', 'span', 'table',
    'thead', 'tbody', 'tr', 'th', 'td'
  ],
  ALLOWED_ATTR: ['href', 'title', 'class'],
  // defense in depth: no data:/javascript: URIs
  ALLOWED_URI_REGEXP: /^(?:https?|mailto):/i
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

export function renderMarkdown(text: string): string {
  const src = text ?? ''
  if (typeof DOMPurify.sanitize !== 'function') {
    // No DOM (SSR): don't emit unsanitized HTML — escape and preserve breaks.
    return `<p>${escapeHtml(src).replace(/\n/g, '<br>')}</p>`
  }
  const raw = marked.parse(src, { async: false }) as string
  return DOMPurify.sanitize(raw, SANITIZE_CONFIG)
}
