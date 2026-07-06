package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	neo4jdriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j/dbtype"
)

// handleRenderTool runs a read-only Cypher query and writes a standalone
// cytoscape.js HTML page containing every node and relationship in the result.
// Same read-only enforcement as handleCypherTool (regex pre-check + read-only
// transaction + caps), but row_limit and timeout caps are higher because
// visualization typically wants more of the graph than a JSON answer.
func (s *CodeGraphMCPServer) handleRenderTool(parentCtx context.Context, args map[string]interface{}) ToolCallResponse {
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return errorResponse("render: query is required")
	}

	stripped := stripCypherComments(query)
	if writeKeywordRegex.MatchString(stripped) {
		return errorResponse("render: write keywords (CREATE/MERGE/DELETE/SET/REMOVE/DROP/FOREACH/LOAD CSV) are not allowed.")
	}

	timeoutMs := 5000
	if t, ok := args["timeout_ms"].(float64); ok {
		timeoutMs = int(t)
	}
	if timeoutMs < 100 {
		timeoutMs = 100
	}
	if timeoutMs > 10000 {
		timeoutMs = 10000
	}

	rowLimit := 1000
	if r, ok := args["row_limit"].(float64); ok {
		rowLimit = int(r)
	}
	if rowLimit < 1 {
		rowLimit = 1000
	}
	if rowLimit > 5000 {
		rowLimit = 5000
	}

	layout, _ := args["layout"].(string)
	if layout == "" {
		layout = "fcose"
	}
	switch layout {
	case "fcose", "cose", "concentric", "circle", "grid", "breadthfirst":
	default:
		return errorResponse(fmt.Sprintf("render: unsupported layout %q", layout))
	}

	title, _ := args["title"].(string)
	if title == "" {
		title = "codegraph render"
	}

	outPath, _ := args["out_path"].(string)
	if outPath == "" {
		outPath = fmt.Sprintf("/tmp/codegraph-render-%d.html", time.Now().Unix())
	}
	if !strings.HasSuffix(outPath, ".html") {
		return errorResponse("render: out_path must end in .html")
	}

	ctx, cancel := context.WithTimeout(parentCtx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	type renderNode struct {
		ID    string                 `json:"id"`
		Label string                 `json:"label"`
		Name  string                 `json:"name"`
		Props map[string]interface{} `json:"props"`
	}
	type renderEdge struct {
		ID     string `json:"id"`
		Source string `json:"source"`
		Target string `json:"target"`
		Type   string `json:"type"`
	}
	type renderData struct {
		nodes     map[string]renderNode
		edges     map[string]renderEdge
		truncated bool
	}

	collect := func(v interface{}, data *renderData) {
		switch x := v.(type) {
		case dbtype.Node:
			label := ""
			if len(x.Labels) > 0 {
				label = x.Labels[0]
			}
			name := ""
			if n, ok := x.Props["name"].(string); ok {
				name = n
			} else if p, ok := x.Props["path"].(string); ok {
				name = p
			}
			data.nodes[x.ElementId] = renderNode{
				ID:    x.ElementId,
				Label: label,
				Name:  name,
				Props: x.Props,
			}
		case dbtype.Relationship:
			data.edges[x.ElementId] = renderEdge{
				ID:     x.ElementId,
				Source: x.StartElementId,
				Target: x.EndElementId,
				Type:   x.Type,
			}
		}
	}

	result, err := s.client.ExecuteRead(ctx, func(tx neo4jdriver.ManagedTransaction) (any, error) {
		res, err := tx.Run(ctx, query, map[string]any{})
		if err != nil {
			return nil, err
		}
		keys, _ := res.Keys()
		out := &renderData{nodes: map[string]renderNode{}, edges: map[string]renderEdge{}}
		rows := 0
		for res.Next(ctx) {
			if rows >= rowLimit {
				out.truncated = true
				break
			}
			rows++
			rec := res.Record()
			for _, k := range keys {
				v, _ := rec.Get(k)
				switch vv := v.(type) {
				case []interface{}:
					for _, item := range vv {
						collect(item, out)
					}
				default:
					collect(v, out)
				}
			}
		}
		if out.truncated {
			for res.Next(ctx) {
			}
		}
		if rerr := res.Err(); rerr != nil {
			return out, rerr
		}
		return out, nil
	})

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return errorResponse(fmt.Sprintf("render: query timed out after %dms", timeoutMs))
		}
		return errorResponse(fmt.Sprintf("render: %v", err))
	}

	data, _ := result.(*renderData)
	if data == nil {
		return errorResponse("render: internal error (nil result)")
	}

	nodes := make([]renderNode, 0, len(data.nodes))
	for _, n := range data.nodes {
		nodes = append(nodes, n)
	}
	edges := make([]renderEdge, 0, len(data.edges))
	for _, e := range data.edges {
		// Drop dangling edges whose endpoints didn't make it into the node set.
		if _, ok := data.nodes[e.Source]; !ok {
			continue
		}
		if _, ok := data.nodes[e.Target]; !ok {
			continue
		}
		edges = append(edges, e)
	}

	labelCounts := map[string]int{}
	for _, n := range nodes {
		labelCounts[n.Label]++
	}
	typeCounts := map[string]int{}
	for _, e := range edges {
		typeCounts[e.Type]++
	}

	nodesJSON, _ := json.Marshal(nodes)
	edgesJSON, _ := json.Marshal(edges)

	html := buildRenderHTML(title, layout, string(nodesJSON), string(edgesJSON))
	if err := os.WriteFile(outPath, []byte(html), 0644); err != nil {
		return errorResponse(fmt.Sprintf("render: write %s: %v", outPath, err))
	}

	summary := map[string]interface{}{
		"file_path":   outPath,
		"node_count":  len(nodes),
		"edge_count":  len(edges),
		"node_labels": labelCounts,
		"rel_types":   typeCounts,
		"truncated":   data.truncated,
		"layout":      layout,
		"hint":        fmt.Sprintf("open file://%s in a browser", outPath),
	}
	body, _ := json.MarshalIndent(summary, "", "  ")
	return ToolCallResponse{Content: []ToolContent{{Type: "text", Text: string(body)}}}
}

// buildRenderHTML produces a self-contained cytoscape.js page. The JS strings
// are JSON-encoded element arrays inlined into the template.
func buildRenderHTML(title, layout, nodesJSON, edgesJSON string) string {
	const tmpl = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{{TITLE}}</title>
<style>
  html,body { margin:0; padding:0; height:100%; font-family: -apple-system, system-ui, sans-serif; background:#0f1117; color:#e6e6e6; }
  #app { display:flex; height:100%; }
  #cy { flex:1; background:#0f1117; }
  #side { width:360px; background:#171a23; border-left:1px solid #2a2f3a; padding:14px 16px; overflow:auto; }
  #side h2 { margin:0 0 8px; font-size:13px; font-weight:600; letter-spacing:.02em; text-transform:uppercase; color:#8a93a6; }
  #side .muted { color:#8a93a6; font-size:12px; }
  #legend { margin-top:8px; }
  #legend .row { display:flex; align-items:center; gap:8px; margin:4px 0; font-size:12px; }
  #legend .sw { width:12px; height:12px; border-radius:3px; }
  #info { font-size:12px; }
  #info pre { background:#0f1117; padding:8px; border-radius:4px; overflow:auto; max-height:280px; font-size:11px; line-height:1.4; }
  #controls { margin-top:14px; display:flex; flex-direction:column; gap:6px; }
  #controls button, #controls select { background:#222836; color:#e6e6e6; border:1px solid #2a2f3a; padding:6px 10px; border-radius:4px; font-size:12px; cursor:pointer; }
  #controls button:hover, #controls select:hover { background:#2a2f3a; }
  #header { padding:10px 14px; border-bottom:1px solid #2a2f3a; display:flex; justify-content:space-between; align-items:baseline; }
  #header h1 { margin:0; font-size:14px; font-weight:600; }
  #stats { font-size:11px; color:#8a93a6; }
  .section { margin-bottom:18px; }
</style>
<script src="https://unpkg.com/cytoscape@3.30.1/dist/cytoscape.min.js"></script>
<script src="https://unpkg.com/layout-base/layout-base.js"></script>
<script src="https://unpkg.com/cose-base/cose-base.js"></script>
<script src="https://unpkg.com/cytoscape-fcose/cytoscape-fcose.js"></script>
</head>
<body>
<div id="app">
  <div style="flex:1; display:flex; flex-direction:column;">
    <div id="header"><h1>{{TITLE}}</h1><div id="stats"></div></div>
    <div id="cy"></div>
  </div>
  <div id="side">
    <div class="section">
      <h2>Inspector</h2>
      <div id="info"><div class="muted">Click a node or edge.</div></div>
    </div>
    <div class="section">
      <h2>Controls</h2>
      <div id="controls">
        <select id="layout">
          <option value="fcose">fcose (force) — default</option>
          <option value="cose">cose</option>
          <option value="concentric">concentric (by degree)</option>
          <option value="circle">circle</option>
          <option value="grid">grid</option>
          <option value="breadthfirst">breadthfirst</option>
        </select>
        <button id="fit">Fit to view</button>
        <button id="reset">Reset zoom</button>
        <button id="labels">Toggle edge labels</button>
      </div>
    </div>
    <div class="section">
      <h2>Legend</h2>
      <div id="legend"></div>
    </div>
  </div>
</div>
<script>
  if (typeof cytoscape !== "undefined" && typeof cytoscapeFcose !== "undefined") {
    cytoscape.use(cytoscapeFcose);
  }

  const nodes = {{NODES_JSON}};
  const edges = {{EDGES_JSON}};
  const initialLayout = {{LAYOUT}};

  // Strip the longest common path prefix from all node names so labels stay
  // legible when (e.g.) every Service starts with "codegraph/".
  function commonPrefix(strs) {
    if (strs.length < 2) return "";
    let p = strs[0];
    for (const s of strs) { while (!s.startsWith(p)) p = p.slice(0, -1); if (!p) return ""; }
    const cut = p.lastIndexOf("/");
    return cut >= 0 ? p.slice(0, cut + 1) : "";
  }
  const allNames = nodes.map(n => n.name || "").filter(Boolean);
  const prefix = commonPrefix(allNames);
  function shortName(n) {
    let s = n.name || n.id;
    if (prefix && s.startsWith(prefix)) s = s.slice(prefix.length);
    if (s.length > 32) s = s.slice(0, 30) + "…";
    return s;
  }

  // Choose color axis. Multi-label graphs: color by label. Single-label graphs:
  // color by degree tier (orphan / low / mid / high) so structure isn't lost.
  const labels = [...new Set(nodes.map(n => n.label || "Node"))];
  const palette = ["#4c8bf5","#ef5b9c","#f0b400","#26c6da","#9ccc65","#ab47bc","#ff7043","#26a69a","#7e57c2","#ec407a","#5c6bc0","#d4e157"];
  const labelColors = {};
  labels.forEach((l,i) => labelColors[l] = palette[i % palette.length]);

  // Pre-compute degree per node id for sizing (and the single-label color tier).
  const degree = {};
  for (const n of nodes) degree[n.id] = 0;
  for (const e of edges) { degree[e.source] = (degree[e.source]||0)+1; degree[e.target] = (degree[e.target]||0)+1; }
  const maxDeg = Math.max(1, ...Object.values(degree));

  const singleLabel = labels.length === 1;
  const tierColors = ["#3a4256","#5c6bc0","#26a69a","#f0b400","#ef5b9c"]; // 0 = orphan, 4 = hub
  function tierFor(d) {
    if (d === 0) return 0;
    if (d <= 2) return 1;
    if (d <= 5) return 2;
    if (d <= 10) return 3;
    return 4;
  }
  function colorFor(node) {
    if (singleLabel) return tierColors[tierFor(degree[node.id] || 0)];
    return labelColors[node.label || "Node"];
  }

  const elements = [];
  for (const n of nodes) {
    elements.push({ data: {
      id: n.id, label: n.label || "Node",
      name: n.name || n.id, short: shortName(n),
      degree: degree[n.id] || 0,
      color: colorFor(n),
      props: n.props || {}
    }});
  }
  for (const e of edges) {
    elements.push({ data: { id: e.id, source: e.source, target: e.target, label: e.type } });
  }

  // Size: log-scaled by degree, dramatic enough to read structurally
  function sizeFor(d) { return 22 + Math.round(Math.sqrt(d) * 12); }

  const cy = cytoscape({
    container: document.getElementById("cy"),
    elements,
    wheelSensitivity: 0.2,
    minZoom: 0.1,
    maxZoom: 4,
    style: [
      { selector: "node", style: {
        "background-color": "data(color)",
        "label": "data(short)",
        "font-size": 11,
        "font-weight": 500,
        "color": "#ffffff",
        "text-outline-color": "#0f1117",
        "text-outline-width": 3,
        "text-valign": "center",
        "text-halign": "center",
        "text-wrap": "ellipsis",
        "text-max-width": 160,
        "width":  ele => sizeFor(ele.data("degree")),
        "height": ele => sizeFor(ele.data("degree")),
        "border-color": "#0f1117",
        "border-width": 2
      }},
      { selector: "edge", style: {
        "width": 1.2,
        "line-color": "#3a4256",
        "target-arrow-color": "#5a6378",
        "target-arrow-shape": "triangle",
        "arrow-scale": 0.9,
        "curve-style": "bezier",
        "label": "data(label)",
        "font-size": 8,
        "color": "#6a7388",
        "text-rotation": "autorotate",
        "text-background-color": "#0f1117",
        "text-background-opacity": 0.85,
        "text-background-padding": 2,
        "opacity": 0.65
      }},
      { selector: "node:selected", style: { "border-color": "#fff", "border-width": 3 } },
      { selector: "edge:selected", style: { "line-color": "#fff", "target-arrow-color": "#fff", "opacity": 1, "width": 2 } },
      { selector: ".dim", style: { "opacity": 0.15 } }
    ],
    layout: layoutOpts(initialLayout)
  });

  function layoutOpts(name) {
    const base = { name, animate: true, animationDuration: 500, fit: true, padding: 40 };
    if (name === "fcose") {
      return Object.assign(base, {
        quality: "proof",
        randomize: true,
        nodeRepulsion: 9000,
        idealEdgeLength: 140,
        edgeElasticity: 0.45,
        nestingFactor: 0.1,
        gravity: 0.25,
        gravityRange: 3.0,
        numIter: 2500,
        tile: true,             // pack disconnected components into a grid
        tilingPaddingHorizontal: 30,
        tilingPaddingVertical: 30
      });
    }
    if (name === "cose") {
      return Object.assign(base, { nodeRepulsion: 8000, idealEdgeLength: 120, gravity: 0.25, numIter: 1800, componentSpacing: 80 });
    }
    if (name === "concentric") {
      return Object.assign(base, { concentric: n => n.data("degree"), levelWidth: () => 1, minNodeSpacing: 30 });
    }
    return base;
  }

  // If the requested initial layout is fcose but plugin failed to load, fall back.
  const requested = (typeof cytoscape !== "undefined" && cy.layout({name: initialLayout}).options) ? initialLayout : "cose";
  cy.layout(layoutOpts(requested)).run();

  // Header stats + legend
  document.getElementById("stats").textContent =
    nodes.length + " nodes • " + edges.length + " edges" + (prefix ? " • prefix “" + prefix + "” hidden" : "");
  const legend = document.getElementById("legend");
  if (singleLabel) {
    const tiers = [
      ["orphan (0)",     0],
      ["1-2 edges",      1],
      ["3-5 edges",      2],
      ["6-10 edges",     3],
      ["11+ edges (hub)",4],
    ];
    for (const [name, t] of tiers) {
      const row = document.createElement("div");
      row.className = "row";
      row.innerHTML = '<span class="sw" style="background:'+tierColors[t]+'"></span><span>'+name+'</span>';
      legend.appendChild(row);
    }
  } else {
    for (const lbl of labels) {
      const count = nodes.filter(n => (n.label||"Node") === lbl).length;
      const row = document.createElement("div");
      row.className = "row";
      row.innerHTML = '<span class="sw" style="background:'+labelColors[lbl]+'"></span><span>'+lbl+'</span><span class="muted">('+count+')</span>';
      legend.appendChild(row);
    }
  }

  // Inspector
  cy.on("tap", "node", evt => {
    const d = evt.target.data();
    const props = JSON.stringify(d.props, null, 2);
    document.getElementById("info").innerHTML =
      "<div><strong>" + escapeHTML(d.label) + "</strong></div>" +
      "<div style='margin:4px 0 8px; word-break:break-all;'>" + escapeHTML(d.name) + "</div>" +
      "<div class='muted'>degree " + d.degree + "</div>" +
      "<pre>" + escapeHTML(props) + "</pre>";
    // dim the rest
    cy.elements().addClass("dim");
    const ego = evt.target.closedNeighborhood();
    ego.removeClass("dim");
  });
  cy.on("tap", "edge", evt => {
    const d = evt.target.data();
    document.getElementById("info").innerHTML =
      "<div><strong>" + escapeHTML(d.label) + "</strong></div>" +
      "<div class='muted'>" + escapeHTML(d.source) + " → " + escapeHTML(d.target) + "</div>";
  });
  cy.on("tap", evt => {
    if (evt.target === cy) {
      document.getElementById("info").innerHTML = '<div class="muted">Click a node or edge.</div>';
      cy.elements().removeClass("dim");
    }
  });

  document.getElementById("layout").value = requested;
  document.getElementById("layout").addEventListener("change", e => cy.layout(layoutOpts(e.target.value)).run());
  document.getElementById("fit").addEventListener("click", () => cy.fit(null, 40));
  document.getElementById("reset").addEventListener("click", () => { cy.fit(null, 40); cy.elements().removeClass("dim"); });
  let edgeLabels = true;
  document.getElementById("labels").addEventListener("click", () => {
    edgeLabels = !edgeLabels;
    cy.style().selector("edge").style("label", edgeLabels ? "data(label)" : "").update();
  });

  function escapeHTML(s) { return String(s).replace(/[&<>"']/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;","\"":"&quot;","'":"&#39;"}[c])); }
</script>
</body>
</html>`
	out := strings.ReplaceAll(tmpl, "{{TITLE}}", htmlEscape(title))
	out = strings.ReplaceAll(out, "{{NODES_JSON}}", nodesJSON)
	out = strings.ReplaceAll(out, "{{EDGES_JSON}}", edgesJSON)
	layoutJSON, _ := json.Marshal(layout)
	out = strings.ReplaceAll(out, "{{LAYOUT}}", string(layoutJSON))
	return out
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&#39;")
	return r.Replace(s)
}
