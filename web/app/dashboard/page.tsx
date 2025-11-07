'use client'

import { useSuspenseQuery } from '@apollo/experimental-nextjs-app-support/ssr'
import Link from 'next/link'
import { GET_SERVICES } from '@/lib/graphql/queries'
import type { GetServicesResponse } from '@/types/graphql'

export default function DashboardPage() {
  const { data, error } = useSuspenseQuery<GetServicesResponse>(GET_SERVICES)
  const loading = false // useSuspenseQuery handles loading automatically

  return (
    <div className="min-h-screen bg-gray-50 dark:bg-gray-900">
      {/* Header */}
      <header className="bg-white dark:bg-gray-800 shadow">
        <div className="container mx-auto px-4 py-6">
          <div className="flex items-center justify-between">
            <div>
              <Link href="/" className="text-2xl font-bold text-blue-600 dark:text-blue-400 hover:text-blue-700">
                CodeGraph
              </Link>
              <p className="text-sm text-gray-600 dark:text-gray-400 mt-1">Dashboard</p>
            </div>
            <nav className="flex space-x-4">
              <Link
                href="/dashboard"
                className="px-4 py-2 bg-blue-600 text-white rounded-md font-medium"
              >
                Dashboard
              </Link>
              <Link
                href="/graph"
                className="px-4 py-2 text-gray-700 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700 rounded-md font-medium"
              >
                Graph
              </Link>
            </nav>
          </div>
        </div>
      </header>

      {/* Main Content */}
      <main className="container mx-auto px-4 py-8">
        <div className="mb-8">
          <h1 className="text-3xl font-bold text-gray-900 dark:text-white mb-2">
            Indexed Services
          </h1>
          <p className="text-gray-600 dark:text-gray-400">
            Overview of all services indexed in the code graph
          </p>
        </div>

        {/* Loading State */}
        {loading && (
          <div className="flex items-center justify-center py-12">
            <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-blue-600"></div>
          </div>
        )}

        {/* Error State */}
        {error && (
          <div className="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg p-6">
            <h3 className="text-lg font-semibold text-red-800 dark:text-red-300 mb-2">
              Error loading services
            </h3>
            <p className="text-red-600 dark:text-red-400">{error.message}</p>
            <p className="text-sm text-red-500 dark:text-red-400 mt-2">
              Make sure the GraphQL server is running at http://localhost:8080/graphql
            </p>
          </div>
        )}

        {/* Empty State */}
        {!loading && !error && (!data?.services || data.services.length === 0) && (
          <div className="bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-lg p-12 text-center">
            <div className="text-6xl mb-4">📦</div>
            <h3 className="text-xl font-semibold text-gray-900 dark:text-white mb-2">
              No services indexed yet
            </h3>
            <p className="text-gray-600 dark:text-gray-400 mb-4">
              Index your first codebase to get started
            </p>
            <code className="bg-gray-100 dark:bg-gray-700 px-4 py-2 rounded text-sm">
              make index-self-scip
            </code>
          </div>
        )}

        {/* Services Grid */}
        {!loading && !error && data?.services && data.services.length > 0 && (
          <div className="grid md:grid-cols-2 lg:grid-cols-3 gap-6">
            {data.services.map((service) => (
              <Link
                key={service.name}
                href={`/graph?service=${encodeURIComponent(service.name)}`}
                className="group"
              >
                <div className="bg-white dark:bg-gray-800 rounded-lg shadow-md hover:shadow-xl transition-shadow border border-gray-200 dark:border-gray-700 p-6">
                  {/* Service Header */}
                  <div className="flex items-start justify-between mb-4">
                    <div className="flex-1">
                      <h3 className="text-lg font-semibold text-gray-900 dark:text-white group-hover:text-blue-600 dark:group-hover:text-blue-400 transition-colors">
                        {service.name}
                      </h3>
                      {service.version && (
                        <span className="text-sm text-gray-500 dark:text-gray-400">
                          v{service.version}
                        </span>
                      )}
                    </div>
                    <span className="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-300">
                      {service.language}
                    </span>
                  </div>

                  {/* Package Name */}
                  {service.packageName && (
                    <p className="text-sm text-gray-600 dark:text-gray-400 mb-3 truncate">
                      {service.packageName}
                    </p>
                  )}

                  {/* Stats */}
                  <div className="grid grid-cols-2 gap-4 mb-3">
                    <div>
                      <div className="text-2xl font-bold text-gray-900 dark:text-white">
                        {service.fileCount}
                      </div>
                      <div className="text-xs text-gray-500 dark:text-gray-400">Files</div>
                    </div>
                    <div>
                      <div className="text-2xl font-bold text-gray-900 dark:text-white">
                        {service.symbolCount}
                      </div>
                      <div className="text-xs text-gray-500 dark:text-gray-400">Symbols</div>
                    </div>
                  </div>

                  {/* Indexed At */}
                  <div className="text-xs text-gray-500 dark:text-gray-400 pt-3 border-t border-gray-200 dark:border-gray-700">
                    Indexed {new Date(service.indexedAt).toLocaleDateString()}
                  </div>

                  {/* Repository URL */}
                  {service.repositoryURL && (
                    <div className="mt-2">
                      <a
                        href={service.repositoryURL}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="text-xs text-blue-600 dark:text-blue-400 hover:underline"
                        onClick={(e) => e.stopPropagation()}
                      >
                        View Repository →
                      </a>
                    </div>
                  )}
                </div>
              </Link>
            ))}
          </div>
        )}

        {/* Stats Summary */}
        {!loading && !error && data?.services && data.services.length > 0 && (
          <div className="mt-8 bg-white dark:bg-gray-800 rounded-lg shadow-md border border-gray-200 dark:border-gray-700 p-6">
            <h2 className="text-lg font-semibold text-gray-900 dark:text-white mb-4">
              Summary
            </h2>
            <div className="grid md:grid-cols-3 gap-6">
              <div>
                <div className="text-3xl font-bold text-blue-600 dark:text-blue-400">
                  {data.services.length}
                </div>
                <div className="text-sm text-gray-600 dark:text-gray-400">Total Services</div>
              </div>
              <div>
                <div className="text-3xl font-bold text-green-600 dark:text-green-400">
                  {data.services.reduce((sum, s) => sum + s.fileCount, 0)}
                </div>
                <div className="text-sm text-gray-600 dark:text-gray-400">Total Files</div>
              </div>
              <div>
                <div className="text-3xl font-bold text-purple-600 dark:text-purple-400">
                  {data.services.reduce((sum, s) => sum + s.symbolCount, 0)}
                </div>
                <div className="text-sm text-gray-600 dark:text-gray-400">Total Symbols</div>
              </div>
            </div>
          </div>
        )}
      </main>
    </div>
  )
}
