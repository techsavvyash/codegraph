'use client'

import { useEffect, useRef, useState } from 'react'
import cytoscape, { Core, EdgeDefinition, NodeDefinition } from 'cytoscape'
import dagre from 'cytoscape-dagre'
import fcose from 'cytoscape-fcose'
import cola from 'cytoscape-cola'

// Register layout extensions
if (typeof cytoscape !== 'undefined') {
  cytoscape.use(dagre)
  cytoscape.use(fcose)
  cytoscape.use(cola)
}

export interface GraphNode {
  id: string
  type: string
  label: string
  properties?: string
}

export interface GraphEdge {
  id: string
  source: string
  target: string
  type: string
  properties?: string
}

export interface CodeGraphProps {
  nodes: GraphNode[]
  edges: GraphEdge[]
  layout?: 'dagre' | 'fcose' | 'cola' | 'cose' | 'circle' | 'grid'
  onNodeClick?: (node: GraphNode) => void
  onEdgeClick?: (edge: GraphEdge) => void
}

export default function CodeGraph({
  nodes,
  edges,
  layout = 'dagre',
  onNodeClick,
  onEdgeClick,
}: CodeGraphProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const cyRef = useRef<Core | null>(null)
  const [selectedNode, setSelectedNode] = useState<GraphNode | null>(null)

  useEffect(() => {
    if (!containerRef.current || nodes.length === 0) return

    // Create Cytoscape instance
    const cy = cytoscape({
      container: containerRef.current,
      elements: [
        ...nodes.map((node): NodeDefinition => ({
          data: {
            id: node.id,
            label: node.label,
            type: node.type,
            properties: node.properties,
          },
        })),
        ...edges.map((edge): EdgeDefinition => ({
          data: {
            id: edge.id,
            source: edge.source,
            target: edge.target,
            label: edge.type,
            type: edge.type,
            properties: edge.properties,
          },
        })),
      ],
      style: [
        {
          selector: 'node',
          style: {
            'background-color': (ele: any) => {
              const type = ele.data('type')
              switch (type) {
                case 'Service':
                  return '#3B82F6' // blue
                case 'File':
                  return '#10B981' // green
                case 'Symbol':
                  return '#8B5CF6' // purple
                case 'Function':
                  return '#F59E0B' // amber
                case 'Class':
                  return '#EF4444' // red
                default:
                  return '#6B7280' // gray
              }
            },
            label: 'data(label)',
            'text-valign': 'center',
            'text-halign': 'center',
            color: '#ffffff',
            'font-size': '12px',
            'font-weight': 'bold',
            width: (ele: any) => {
              const type = ele.data('type')
              return type === 'Service' ? 60 : type === 'File' ? 50 : 40
            },
            height: (ele: any) => {
              const type = ele.data('type')
              return type === 'Service' ? 60 : type === 'File' ? 50 : 40
            },
            'border-width': 2,
            'border-color': '#ffffff',
          },
        },
        {
          selector: 'node:selected',
          style: {
            'border-width': 4,
            'border-color': '#FBBF24',
            'background-color': '#1E40AF',
          },
        },
        {
          selector: 'edge',
          style: {
            width: 2,
            'line-color': '#9CA3AF',
            'target-arrow-color': '#9CA3AF',
            'target-arrow-shape': 'triangle',
            'curve-style': 'bezier',
            label: 'data(label)',
            'font-size': '10px',
            color: '#6B7280',
            'text-rotation': 'autorotate',
            'text-margin-y': -10,
          },
        },
        {
          selector: 'edge:selected',
          style: {
            width: 3,
            'line-color': '#3B82F6',
            'target-arrow-color': '#3B82F6',
          },
        },
      ],
      layout: {
        name: layout,
        ...(layout === 'dagre' && {
          rankDir: 'TB',
          nodeSep: 50,
          rankSep: 100,
        }),
        ...(layout === 'fcose' && {
          quality: 'default',
          randomize: false,
          animate: true,
          animationDuration: 1000,
        }),
        ...(layout === 'cola' && {
          animate: true,
          randomize: false,
          maxSimulationTime: 2000,
        }),
      },
      minZoom: 0.1,
      maxZoom: 3,
      wheelSensitivity: 0.2,
    })

    // Event handlers
    cy.on('tap', 'node', (evt) => {
      const node = evt.target
      const nodeData = {
        id: node.data('id'),
        type: node.data('type'),
        label: node.data('label'),
        properties: node.data('properties'),
      }
      setSelectedNode(nodeData)
      if (onNodeClick) {
        onNodeClick(nodeData)
      }
    })

    cy.on('tap', 'edge', (evt) => {
      const edge = evt.target
      const edgeData = {
        id: edge.data('id'),
        source: edge.data('source'),
        target: edge.data('target'),
        type: edge.data('type'),
        properties: edge.data('properties'),
      }
      if (onEdgeClick) {
        onEdgeClick(edgeData)
      }
    })

    // Fit to viewport
    cy.fit(undefined, 50)

    cyRef.current = cy

    return () => {
      cy.destroy()
    }
  }, [nodes, edges, layout, onNodeClick, onEdgeClick])

  // Zoom controls
  const handleZoomIn = () => {
    if (cyRef.current) {
      cyRef.current.zoom(cyRef.current.zoom() * 1.2)
      cyRef.current.center()
    }
  }

  const handleZoomOut = () => {
    if (cyRef.current) {
      cyRef.current.zoom(cyRef.current.zoom() * 0.8)
      cyRef.current.center()
    }
  }

  const handleFit = () => {
    if (cyRef.current) {
      cyRef.current.fit(undefined, 50)
    }
  }

  return (
    <div className="relative w-full h-full">
      {/* Cytoscape Container */}
      <div
        ref={containerRef}
        className="cytoscape-container w-full h-full bg-gray-50 dark:bg-gray-900 rounded-lg"
      />

      {/* Zoom Controls */}
      <div className="absolute top-4 right-4 flex flex-col space-y-2">
        <button
          onClick={handleZoomIn}
          className="p-2 bg-white dark:bg-gray-800 rounded-md shadow-lg hover:bg-gray-100 dark:hover:bg-gray-700 border border-gray-200 dark:border-gray-700"
          title="Zoom In"
        >
          <svg
            className="w-5 h-5 text-gray-700 dark:text-gray-300"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth={2}
              d="M12 4v16m8-8H4"
            />
          </svg>
        </button>
        <button
          onClick={handleZoomOut}
          className="p-2 bg-white dark:bg-gray-800 rounded-md shadow-lg hover:bg-gray-100 dark:hover:bg-gray-700 border border-gray-200 dark:border-gray-700"
          title="Zoom Out"
        >
          <svg
            className="w-5 h-5 text-gray-700 dark:text-gray-300"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
          >
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M20 12H4" />
          </svg>
        </button>
        <button
          onClick={handleFit}
          className="p-2 bg-white dark:bg-gray-800 rounded-md shadow-lg hover:bg-gray-100 dark:hover:bg-gray-700 border border-gray-200 dark:border-gray-700"
          title="Fit to Screen"
        >
          <svg
            className="w-5 h-5 text-gray-700 dark:text-gray-300"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth={2}
              d="M4 8V4m0 0h4M4 4l5 5m11-1V4m0 0h-4m4 0l-5 5M4 16v4m0 0h4m-4 0l5-5m11 5l-5-5m5 5v-4m0 4h-4"
            />
          </svg>
        </button>
      </div>

      {/* Legend */}
      <div className="absolute bottom-4 left-4 bg-white dark:bg-gray-800 rounded-md shadow-lg p-4 border border-gray-200 dark:border-gray-700">
        <h4 className="text-sm font-semibold text-gray-900 dark:text-white mb-2">Legend</h4>
        <div className="space-y-1 text-xs">
          <div className="flex items-center space-x-2">
            <div className="w-4 h-4 rounded-full bg-blue-500"></div>
            <span className="text-gray-700 dark:text-gray-300">Service</span>
          </div>
          <div className="flex items-center space-x-2">
            <div className="w-4 h-4 rounded-full bg-green-500"></div>
            <span className="text-gray-700 dark:text-gray-300">File</span>
          </div>
          <div className="flex items-center space-x-2">
            <div className="w-4 h-4 rounded-full bg-purple-500"></div>
            <span className="text-gray-700 dark:text-gray-300">Symbol</span>
          </div>
          <div className="flex items-center space-x-2">
            <div className="w-4 h-4 rounded-full bg-amber-500"></div>
            <span className="text-gray-700 dark:text-gray-300">Function</span>
          </div>
        </div>
      </div>

      {/* Selected Node Info */}
      {selectedNode && (
        <div className="absolute top-4 left-4 bg-white dark:bg-gray-800 rounded-md shadow-lg p-4 max-w-sm border border-gray-200 dark:border-gray-700">
          <div className="flex items-center justify-between mb-2">
            <h4 className="text-sm font-semibold text-gray-900 dark:text-white">
              Selected Node
            </h4>
            <button
              onClick={() => setSelectedNode(null)}
              className="text-gray-400 hover:text-gray-600 dark:hover:text-gray-300"
            >
              ✕
            </button>
          </div>
          <div className="space-y-1 text-xs">
            <div>
              <span className="font-medium text-gray-700 dark:text-gray-300">Type:</span>{' '}
              <span className="text-gray-600 dark:text-gray-400">{selectedNode.type}</span>
            </div>
            <div>
              <span className="font-medium text-gray-700 dark:text-gray-300">Label:</span>{' '}
              <span className="text-gray-600 dark:text-gray-400">{selectedNode.label}</span>
            </div>
            <div>
              <span className="font-medium text-gray-700 dark:text-gray-300">ID:</span>{' '}
              <span className="text-gray-600 dark:text-gray-400 break-all">
                {selectedNode.id}
              </span>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
