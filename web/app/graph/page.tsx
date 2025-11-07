'use client'

import { useSuspenseQuery, useQuery } from '@apollo/experimental-nextjs-app-support/ssr'
import { useSearchParams } from 'next/navigation'
import Link from 'next/link'
import { useState, useEffect, Suspense } from 'react'
import CodeGraph from '@/components/CodeGraph'
import { GET_SERVICES, GET_GRAPH } from '@/lib/graphql/queries'
import type { GetServicesResponse, GetGraphResponse, GetGraphVariables } from '@/types/graphql'

// Force dynamic rendering
export const dynamic = 'force-dynamic'

function GraphContent() {
  const searchParams = useSearchParams()
  const serviceFromUrl = searchParams.get('service')

  const [selectedService, setSelectedService] = useState<string>(serviceFromUrl || '')
  const [layout, setLayout] = useState<'dagre' | 'fcose' | 'cola' | 'cose' | 'circle' | 'grid'>('dagre')
  const [limit, setLimit] = useState(100)

  const { data: servicesData } = useSuspenseQuery<GetServicesResponse>(GET_SERVICES)

  const { loading, error, data, refetch } = useQuery<GetGraphResponse, GetGraphVariables>(
    GET_GRAPH,
    {
      variables: {
        serviceName: selectedService,
        limit,
        offset: 0,
      },
      skip: !selectedService,
    }
  )

  // Set first service as selected if none is selected
  useEffect(() => {
    if (!selectedService && servicesData?.services && servicesData.services.length > 0) {
      setSelectedService(servicesData.services[0].name)
    }
  }, [servicesData, selectedService])

  const handleServiceChange = (serviceName: string) => {
    setSelectedService(serviceName)
  }

  const handleLayoutChange = (newLayout: typeof layout) => {
    setLayout(newLayout)
  }

  const handleLimitChange = (newLimit: number) => {
    setLimit(newLimit)
  }

  return (
    <div className="min-h-screen bg-gray-50 dark:bg-gray-900 flex flex-col">
      {/* Header */}
      <header className="bg-white dark:bg-gray-800 shadow">
        <div className="container mx-auto px-4 py-4">
          <div className="flex items-center justify-between">
            <div>
              <Link href="/" className="text-2xl font-bold text-blue-600 dark:text-blue-400 hover:text-blue-700">
                CodeGraph
              </Link>
              <p className="text-sm text-gray-600 dark:text-gray-400 mt-1">Interactive Graph</p>
            </div>
            <nav className="flex space-x-4">
              <Link
                href="/dashboard"
                className="px-4 py-2 text-gray-700 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700 rounded-md font-medium"
              >
                Dashboard
              </Link>
              <Link
                href="/graph"
                className="px-4 py-2 bg-blue-600 text-white rounded-md font-medium"
              >
                Graph
              </Link>
            </nav>
          </div>
        </div>
      </header>

      {/* Controls */}
      <div className="bg-white dark:bg-gray-800 border-b border-gray-200 dark:border-gray-700">
        <div className="container mx-auto px-4 py-4">
          <div className="flex flex-wrap items-center gap-4">
            {/* Service Selector */}
            <div className="flex-1 min-w-[200px]">
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                Service
              </label>
              <select
                value={selectedService}
                onChange={(e) => handleServiceChange(e.target.value)}
                className="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-md bg-white dark:bg-gray-700 text-gray-900 dark:text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
              >
                <option value="">Select a service...</option>
                {servicesData?.services.map((service) => (
                  <option key={service.name} value={service.name}>
                    {service.name} ({service.fileCount} files, {service.symbolCount} symbols)
                  </option>
                ))}
              </select>
            </div>

            {/* Layout Selector */}
            <div className="min-w-[150px]">
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                Layout
              </label>
              <select
                value={layout}
                onChange={(e) => handleLayoutChange(e.target.value as typeof layout)}
                className="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-md bg-white dark:bg-gray-700 text-gray-900 dark:text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
              >
                <option value="dagre">Hierarchical (Dagre)</option>
                <option value="fcose">Force-Directed (fCoSE)</option>
                <option value="cola">Constraint-Based (Cola)</option>
                <option value="cose">COSE</option>
                <option value="circle">Circle</option>
                <option value="grid">Grid</option>
              </select>
            </div>

            {/* Limit Selector */}
            <div className="min-w-[120px]">
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                Max Nodes
              </label>
              <select
                value={limit}
                onChange={(e) => handleLimitChange(parseInt(e.target.value))}
                className="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-md bg-white dark:bg-gray-700 text-gray-900 dark:text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
              >
                <option value={50}>50</option>
                <option value={100}>100</option>
                <option value={200}>200</option>
                <option value={500}>500</option>
              </select>
            </div>

            {/* Refresh Button */}
            <div className="flex items-end">
              <button
                onClick={() => refetch()}
                disabled={!selectedService}
                className="px-4 py-2 bg-blue-600 text-white rounded-md hover:bg-blue-700 disabled:bg-gray-400 disabled:cursor-not-allowed font-medium"
              >
                Refresh
              </button>
            </div>
          </div>
        </div>
      </div>

      {/* Main Content - Graph */}
      <div className="flex-1 container mx-auto px-4 py-4" style={{ minHeight: 'calc(100vh - 200px)' }}>
        <div className="h-full bg-white dark:bg-gray-800 rounded-lg shadow-md border border-gray-200 dark:border-gray-700 overflow-hidden">
          {/* Loading State */}
          {loading && (
            <div className="flex items-center justify-center h-full">
              <div className="text-center">
                <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-blue-600 mx-auto mb-4"></div>
                <p className="text-gray-600 dark:text-gray-400">Loading graph...</p>
              </div>
            </div>
          )}

          {/* Error State */}
          {error && (
            <div className="flex items-center justify-center h-full p-8">
              <div className="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg p-6 max-w-lg">
                <h3 className="text-lg font-semibold text-red-800 dark:text-red-300 mb-2">
                  Error loading graph
                </h3>
                <p className="text-red-600 dark:text-red-400">{error.message}</p>
              </div>
            </div>
          )}

          {/* No Service Selected */}
          {!selectedService && !loading && (
            <div className="flex items-center justify-center h-full">
              <div className="text-center">
                <div className="text-6xl mb-4">🔍</div>
                <h3 className="text-xl font-semibold text-gray-900 dark:text-white mb-2">
                  Select a service to view its graph
                </h3>
                <p className="text-gray-600 dark:text-gray-400">
                  Choose a service from the dropdown above
                </p>
              </div>
            </div>
          )}

          {/* Empty Graph */}
          {selectedService && !loading && !error && data?.graph && data.graph.nodes.length === 0 && (
            <div className="flex items-center justify-center h-full">
              <div className="text-center">
                <div className="text-6xl mb-4">📊</div>
                <h3 className="text-xl font-semibold text-gray-900 dark:text-white mb-2">
                  No graph data available
                </h3>
                <p className="text-gray-600 dark:text-gray-400 mb-4">
                  This service doesn't have any indexed graph data yet
                </p>
              </div>
            </div>
          )}

          {/* Graph Visualization */}
          {!loading && !error && data?.graph && data.graph.nodes.length > 0 && (
            <div className="relative w-full" style={{ height: 'calc(100vh - 250px)' }}>
              <CodeGraph
                nodes={data.graph.nodes}
                edges={data.graph.edges}
                layout={layout}
                onNodeClick={(node) => {
                  console.log('Node clicked:', node)
                }}
                onEdgeClick={(edge) => {
                  console.log('Edge clicked:', edge)
                }}
              />

              {/* Stats Overlay */}
              <div className="absolute top-4 left-1/2 transform -translate-x-1/2 bg-white dark:bg-gray-800 rounded-md shadow-lg px-4 py-2 border border-gray-200 dark:border-gray-700">
                <div className="flex items-center space-x-6 text-sm">
                  <div>
                    <span className="font-medium text-gray-700 dark:text-gray-300">Nodes:</span>{' '}
                    <span className="text-gray-900 dark:text-white font-semibold">
                      {data.graph.nodes.length}
                    </span>
                  </div>
                  <div>
                    <span className="font-medium text-gray-700 dark:text-gray-300">Edges:</span>{' '}
                    <span className="text-gray-900 dark:text-white font-semibold">
                      {data.graph.edges.length}
                    </span>
                  </div>
                  {data.graph.hasMore && (
                    <div className="text-amber-600 dark:text-amber-400">
                      More data available - increase limit
                    </div>
                  )}
                </div>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

export default function GraphPage() {
  return (
    <Suspense fallback={<div className="min-h-screen flex items-center justify-center"><div className="animate-spin rounded-full h-12 w-12 border-b-2 border-blue-600"></div></div>}>
      <GraphContent />
    </Suspense>
  )
}
