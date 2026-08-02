/**
 * Pure layering core for the service Overview visualizer. No Svelte, no DOM,
 * no cytoscape — this is where the directory tree, visibility resolution, and
 * edge re-aggregation live so they can be exhaustively unit-tested. The store
 * (overview.svelte.ts) holds the reactive state and calls straight into these.
 *
 * The model works in three layers:
 *   1. buildTree(files) → a directory tree, single-child chains merged.
 *   2. visibleGraph(tree, edges, state, drilldowns) → the render node/edge set
 *      for the current expansion state, with file-pair weights re-aggregated
 *      onto whichever ancestor is actually visible.
 *   3. reducers (toggleDir/expandFile/collapseFile) → pure state transitions.
 */
import type {
  FileEdge,
  FileSymbol,
  OverviewFile,
  RenderEdge,
  RenderNode,
  VisibleGraph
} from '$lib/types/overview'

// ---------------------------------------------------------------------------
// directory tree

export interface DirNode {
  kind: 'dir'
  /** full path of this dir from the root, e.g. "internal/graph" */
  path: string
  /** display label — for a merged chain this is the joined tail, e.g. "web/studio/src" */
  label: string
  dirs: DirNode[]
  files: FileLeaf[]
}

export interface FileLeaf {
  kind: 'file'
  nodeId: string
  /** full service-relative path */
  path: string
  /** basename */
  label: string
  language: string | null
  lineCount: number
  symbolCount: number
}

export interface OverviewTree {
  root: DirNode
  /** every file keyed by its full path — used for visibleFor and edge mapping */
  fileByPath: Map<string, FileLeaf>
  /** full path → owning file (index into the tree) keyed by file nodeId */
  fileById: Map<string, FileLeaf>
}

interface MutableDir {
  path: string
  segment: string
  dirs: Map<string, MutableDir>
  files: FileLeaf[]
}

function newMutableDir(path: string, segment: string): MutableDir {
  return { path, segment, dirs: new Map(), files: [] }
}

/**
 * Builds the directory tree from file paths. A single-child directory chain
 * (a dir whose only content is one subdirectory, all the way down) is merged
 * into one node whose label is the joined segments ("web/studio/src"). Files
 * at the repo root hang directly off the root node. Paths use '/' separators;
 * the service-relative paths from the graph already do.
 */
export function buildTree(files: OverviewFile[]): OverviewTree {
  const root = newMutableDir('', '')
  const fileByPath = new Map<string, FileLeaf>()
  const fileById = new Map<string, FileLeaf>()

  for (const f of files) {
    const parts = f.path.split('/')
    const base = parts[parts.length - 1]
    const dirParts = parts.slice(0, -1)
    let cur = root
    let acc = ''
    for (const seg of dirParts) {
      acc = acc ? `${acc}/${seg}` : seg
      let next = cur.dirs.get(seg)
      if (!next) {
        next = newMutableDir(acc, seg)
        cur.dirs.set(seg, next)
      }
      cur = next
    }
    const leaf: FileLeaf = {
      kind: 'file',
      nodeId: f.nodeId,
      path: f.path,
      label: base,
      language: f.language,
      lineCount: f.lineCount,
      symbolCount: f.symbolCount
    }
    cur.files.push(leaf)
    fileByPath.set(f.path, leaf)
    fileById.set(f.nodeId, leaf)
  }

  return { root: freezeDir(root, true), fileByPath, fileById }
}

/**
 * Converts the mutable dir into the immutable DirNode, applying chain-merge:
 * a non-root dir with one subdir and no files of its own adopts the subdir's
 * contents and joins labels ("web/studio/src"), keeping the DEEPEST path as its
 * id so visibleFor can still walk to it. `isRoot` disables merging for the top
 * node (the root carries its top dirs + any root-level files, never merged away).
 */
function freezeDir(dir: MutableDir, isRoot: boolean): DirNode {
  let path = dir.path
  let label = dir.segment
  let subDirs = [...dir.dirs.values()]
  let files = dir.files

  // Merge a single-child chain: while this dir (non-root) has exactly one
  // subdir and no files, absorb the subdir, extending the label.
  if (!isRoot) {
    while (subDirs.length === 1 && files.length === 0) {
      const only = subDirs[0]
      label = `${label}/${only.segment}`
      path = only.path
      files = only.files
      subDirs = [...only.dirs.values()]
    }
  }

  const dirs = subDirs
    .map((d) => freezeDir(d, false))
    .sort((a, b) => a.label.localeCompare(b.label))
  const fileLeaves = [...files].sort((a, b) => a.label.localeCompare(b.label))

  return {
    kind: 'dir',
    path,
    label: isRoot ? '' : label,
    dirs,
    files: fileLeaves
  }
}

// ---------------------------------------------------------------------------
// visibility state

export interface OverviewVisibility {
  /** dir paths that are expanded (replaced by their children) */
  expandedDirs: Set<string>
  /** file nodeIds whose symbols are shown (file becomes a compound parent) */
  expandedFiles: Set<string>
}

export function emptyVisibility(): OverviewVisibility {
  return { expandedDirs: new Set(), expandedFiles: new Set() }
}

// ---------------------------------------------------------------------------
// visibleFor: the ancestor a path currently renders as

/**
 * Walks from the root toward `path`, descending through expanded dirs. Returns
 * the id of the visible node standing in for that path: the first COLLAPSED dir
 * ancestor, or (if every ancestor dir is expanded) the file itself. Directory
 * ids are prefixed "dir:" and files carry their nodeId — a file and a dir can
 * never collide, and cytoscape gets stable ids.
 *
 * Because chain-merge folds a run of dirs into one node with the deepest path
 * as its id, the intermediate segment paths of a merged chain are NOT valid dir
 * ids. We resolve against the tree so the walk uses real (post-merge) dir nodes.
 */
export function dirId(path: string): string {
  return `dir:${path}`
}

/**
 * Resolves the visible node id for a file path given the current expansion. The
 * walk is over the ACTUAL tree dirs (post chain-merge), so a merged chain
 * expands/collapses as a single unit keyed by its deepest path.
 */
export function visibleFor(tree: OverviewTree, filePath: string, state: OverviewVisibility): string {
  const leaf = tree.fileByPath.get(filePath)
  if (!leaf) return dirId('') // unknown path → root (defensive; edges guard this)

  // Descend the tree toward the file. At each dir, if it's collapsed the file
  // renders as that dir; if expanded, keep going. If we reach the file's own
  // parent dir and it's expanded, the file is visible (as itself or its
  // compound if the file is expanded — the caller handles that distinction).
  let cur = tree.root
  for (;;) {
    // find the child dir on the path to the file
    const childDir = cur.dirs.find((d) => isAncestorPath(d.path, filePath))
    if (childDir) {
      if (!state.expandedDirs.has(childDir.path)) return dirId(childDir.path)
      cur = childDir
      continue
    }
    // no further dir on the path → the file hangs directly off `cur`
    return leaf.nodeId
  }
}

/** True when `dirPath` is a strict ancestor directory of `filePath`. */
function isAncestorPath(dirPath: string, filePath: string): boolean {
  if (dirPath === '') return true
  return filePath === dirPath || filePath.startsWith(dirPath + '/')
}

// ---------------------------------------------------------------------------
// rolled-up stats for a subtree

interface DirStats {
  fileCount: number
  symbolCount: number
}

function subtreeStats(dir: DirNode): DirStats {
  let fileCount = dir.files.length
  let symbolCount = dir.files.reduce((s, f) => s + f.symbolCount, 0)
  for (const sub of dir.dirs) {
    const s = subtreeStats(sub)
    fileCount += s.fileCount
    symbolCount += s.symbolCount
  }
  return { fileCount, symbolCount }
}

// ---------------------------------------------------------------------------
// visibleGraph: the render node/edge set for the current state

/**
 * Produces the nodes and edges to render for the current expansion state.
 *
 * Nodes:
 *  - A collapsed dir is one node carrying rolled-up fileCount/symbolCount.
 *  - An expanded dir is REPLACED by its children (flat — no compound for dirs).
 *  - A collapsed file is one node. An expanded file is a COMPOUND parent whose
 *    children are its drilldown symbols.
 *
 * Edges: see reAggregateEdges. Self-edges (both endpoints the same visible node)
 * are dropped.
 */
export function visibleGraph(
  tree: OverviewTree,
  edges: FileEdge[],
  state: OverviewVisibility,
  drilldowns: Map<string, FileSymbol[]>
): VisibleGraph {
  const nodes: RenderNode[] = []
  emitDirChildren(tree.root, state, drilldowns, nodes, true)

  const renderEdges = reAggregateEdges(tree, edges, state, drilldowns)
  return { nodes, edges: renderEdges }
}

/**
 * Emits the render nodes for one directory's visible children. When `isRoot`,
 * the root's own dirs and files are emitted at top level (the root itself is
 * never a node). For a collapsed subdir we emit a rolled-up dir node; for an
 * expanded subdir we recurse. Files emit as leaf nodes, or compound parents
 * with symbol children when expanded.
 */
function emitDirChildren(
  dir: DirNode,
  state: OverviewVisibility,
  drilldowns: Map<string, FileSymbol[]>,
  out: RenderNode[],
  isRoot: boolean
): void {
  for (const sub of dir.dirs) {
    if (state.expandedDirs.has(sub.path)) {
      emitDirChildren(sub, state, drilldowns, out, false)
    } else {
      const stats = subtreeStats(sub)
      out.push({
        id: dirId(sub.path),
        kind: 'dir',
        label: sub.label,
        fileCount: stats.fileCount,
        symbolCount: stats.symbolCount
      })
    }
  }
  for (const f of dir.files) {
    emitFile(f, state, drilldowns, out)
  }
  void isRoot // kept for call-site clarity; root vs non-root differ only via recursion entry
}

function emitFile(
  f: FileLeaf,
  state: OverviewVisibility,
  drilldowns: Map<string, FileSymbol[]>,
  out: RenderNode[]
): void {
  const expanded = state.expandedFiles.has(f.nodeId)
  out.push({
    id: f.nodeId,
    kind: 'file',
    label: f.label,
    path: f.path,
    symbolCount: f.symbolCount,
    language: f.language,
    lineCount: f.lineCount
  })
  if (expanded) {
    const symbols = drilldowns.get(f.nodeId) ?? []
    for (const s of symbols) {
      out.push({
        id: s.nodeId,
        kind: 'symbol',
        label: s.name,
        parentId: f.nodeId,
        symbolLabel: s.label,
        startLine: s.startLine,
        inCalls: s.inCalls,
        externalOutCalls: s.externalOutCalls
      })
    }
  }
}

// ---------------------------------------------------------------------------
// edge re-aggregation

/**
 * Re-aggregates the file-pair edges onto the currently-visible nodes.
 *
 *  - fnWeight of pair (A,B) rolls onto visible(A)→visible(B) ONLY when file A is
 *    NOT expanded. When A is expanded, its function-level truth comes from the
 *    drilldown symbols instead (below), so double-counting is avoided.
 *  - When file A is expanded: for each of its symbols s with same-service
 *    out-calls, draw s→(target symbol if that target's file is also expanded and
 *    present in drilldown, else s→visible(targetPath)). Duplicate (from,to)
 *    pairs merge, summing weight.
 *  - moduleWeight ALWAYS rolls onto visible(A)→visible(B) (aggregate level),
 *    regardless of expansion — module-scope calls have no per-symbol truth.
 *  - Self-edges (both endpoints the same visible node) are dropped after mapping.
 */
export function reAggregateEdges(
  tree: OverviewTree,
  edges: FileEdge[],
  state: OverviewVisibility,
  drilldowns: Map<string, FileSymbol[]>
): RenderEdge[] {
  const agg = new Map<string, number>() // "src\u0000tgt" → summed aggregate weight
  const sym = new Map<string, number>() // symbol-level, same key space (distinct id prefix avoids collision)

  const bumpAgg = (src: string, tgt: string, w: number) => {
    if (w <= 0 || src === tgt) return
    const k = `${src}\u0000${tgt}`
    agg.set(k, (agg.get(k) ?? 0) + w)
  }
  const bumpSym = (src: string, tgt: string, w: number) => {
    if (w <= 0 || src === tgt) return
    const k = `${src}\u0000${tgt}`
    sym.set(k, (sym.get(k) ?? 0) + w)
  }

  for (const e of edges) {
    const fromLeaf = tree.fileByPath.get(e.fromPath)
    const toLeaf = tree.fileByPath.get(e.toPath)
    if (!fromLeaf || !toLeaf) continue // edge references a file not in this service's set

    const fromExpanded = state.expandedFiles.has(fromLeaf.nodeId)
    const visTo = visibleFor(tree, e.toPath, state)

    // fnWeight: only when the source file is NOT expanded (else drilldown owns it)
    if (!fromExpanded) {
      const visFrom = visibleFor(tree, e.fromPath, state)
      bumpAgg(visFrom, visTo, e.fnWeight)
    }

    // moduleWeight: always at the aggregate level. When from is expanded, the
    // visible source is the compound file node itself (its own nodeId).
    const modFrom = fromExpanded ? fromLeaf.nodeId : visibleFor(tree, e.fromPath, state)
    bumpAgg(modFrom, visTo, e.moduleWeight)
  }

  // Symbol-level edges from expanded files' drilldowns.
  for (const fileId of state.expandedFiles) {
    const symbols = drilldowns.get(fileId)
    if (!symbols) continue
    for (const s of symbols) {
      for (const oc of s.outCalls) {
        const targetLeaf = tree.fileByPath.get(oc.targetPath)
        // Draw symbol→symbol only when the target's file is also expanded AND
        // that exact target symbol is present in its drilldown; otherwise the
        // call terminates at the target's visible aggregate node.
        let tgt: string
        if (targetLeaf && state.expandedFiles.has(targetLeaf.nodeId)) {
          const tSyms = drilldowns.get(targetLeaf.nodeId)
          const present = tSyms?.some((ts) => ts.nodeId === oc.targetId)
          tgt = present ? oc.targetId : visibleFor(tree, oc.targetPath, state)
        } else if (targetLeaf) {
          tgt = visibleFor(tree, oc.targetPath, state)
        } else {
          continue // out-call to a file outside the service set — skip
        }
        bumpSym(s.nodeId, tgt, 1)
      }
    }
  }

  const result: RenderEdge[] = []
  for (const [k, w] of agg) {
    const [source, target] = k.split('\u0000')
    result.push({ id: `agg:${source}->${target}`, source, target, weight: w, kind: 'aggregate' })
  }
  for (const [k, w] of sym) {
    const [source, target] = k.split('\u0000')
    result.push({ id: `sym:${source}->${target}`, source, target, weight: w, kind: 'symbol' })
  }
  return result
}

// ---------------------------------------------------------------------------
// reducers (pure state transitions)

/** Toggles a directory's expansion, returning a new visibility state. */
export function toggleDir(state: OverviewVisibility, path: string): OverviewVisibility {
  const expandedDirs = new Set(state.expandedDirs)
  if (expandedDirs.has(path)) expandedDirs.delete(path)
  else expandedDirs.add(path)
  return { expandedDirs, expandedFiles: new Set(state.expandedFiles) }
}

/** Marks a file expanded (its drilldown symbols become visible). */
export function expandFile(state: OverviewVisibility, fileId: string): OverviewVisibility {
  const expandedFiles = new Set(state.expandedFiles)
  expandedFiles.add(fileId)
  return { expandedDirs: new Set(state.expandedDirs), expandedFiles }
}

/** Collapses a file back to a single node. */
export function collapseFile(state: OverviewVisibility, fileId: string): OverviewVisibility {
  const expandedFiles = new Set(state.expandedFiles)
  expandedFiles.delete(fileId)
  return { expandedDirs: new Set(state.expandedDirs), expandedFiles }
}

// ---------------------------------------------------------------------------
// top connections (for the detail panel)

export interface Connection {
  /** the other node's visible id */
  nodeId: string
  /** its display label, resolved from the render node set */
  label: string
  weight: number
}

/**
 * The strongest in/out edges touching a node in the visible graph, each capped
 * to `limit` and sorted by weight desc. Labels are resolved from the render
 * nodes so the panel shows names, not ids. Pure — trivially testable.
 */
export function topConnections(
  graph: VisibleGraph,
  nodeId: string,
  limit = 5
): { incoming: Connection[]; outgoing: Connection[] } {
  const labelOf = new Map(graph.nodes.map((n) => [n.id, n.label]))
  const outgoing: Connection[] = []
  const incoming: Connection[] = []
  for (const e of graph.edges) {
    if (e.source === nodeId && e.target !== nodeId) {
      outgoing.push({ nodeId: e.target, label: labelOf.get(e.target) ?? e.target, weight: e.weight })
    }
    if (e.target === nodeId && e.source !== nodeId) {
      incoming.push({ nodeId: e.source, label: labelOf.get(e.source) ?? e.source, weight: e.weight })
    }
  }
  const byWeight = (a: Connection, b: Connection) => b.weight - a.weight
  return {
    incoming: incoming.sort(byWeight).slice(0, limit),
    outgoing: outgoing.sort(byWeight).slice(0, limit)
  }
}

// ---------------------------------------------------------------------------
// deep-link codec

/**
 * Encodes the expansion state into an `open=` value: a comma list of expanded
 * dir paths and `f:<nodeId>` entries for expanded files, each URL-encoded so
 * commas/colons in ids survive. Order is dirs-then-files, both sorted, so the
 * URL is stable across renders (no churn in the debounced replaceState).
 */
export function encodeOverviewState(state: OverviewVisibility): string {
  const parts: string[] = []
  for (const d of [...state.expandedDirs].sort()) parts.push(encodeURIComponent(d))
  for (const f of [...state.expandedFiles].sort()) parts.push(encodeURIComponent(`f:${f}`))
  return parts.join(',')
}

/**
 * Decodes an `open=` value back into a visibility state. Malformed or empty
 * input yields empty state — a bad deep link must never crash the page. Entries
 * prefixed `f:` are expanded files; everything else is an expanded dir path.
 */
export function decodeOverviewState(raw: string | null | undefined): OverviewVisibility {
  const state = emptyVisibility()
  if (!raw) return state
  for (const token of raw.split(',')) {
    if (!token) continue
    let decoded: string
    try {
      decoded = decodeURIComponent(token)
    } catch {
      continue // malformed %-escape — skip this entry, keep the rest
    }
    if (decoded.startsWith('f:')) {
      const id = decoded.slice(2)
      if (id) state.expandedFiles.add(id)
    } else {
      state.expandedDirs.add(decoded)
    }
  }
  return state
}
