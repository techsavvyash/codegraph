'use client'

import { useQuery } from '@apollo/experimental-nextjs-app-support/ssr'
import Link from 'next/link'
import { useState } from 'react'
import { SEARCH_SYMBOLS } from '@/lib/graphql/queries'
import type { SearchSymbolsResponse, SearchSymbolsVariables } from '@/types/graphql'

export default function SearchPage() {
  const [searchQuery, setSearchQuery] = useState('')
  const [submittedQuery, setSubmittedQuery] = useState('')

  const { loading, error, data } = useQuery<SearchSymbolsResponse, SearchSymbolsVariables>(
    SEARCH_SYMBOLS,
    {
      variables: {
        query: submittedQuery,
        limit: 50,
      },
      skip: !submittedQuery,
    }
  )

  const handleSearch = (e: React.FormEvent) => {
    e.preventDefault()
    setSubmittedQuery(searchQuery)
  }

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
              <p className="text-sm text-gray-600 dark:text-gray-400 mt-1">Search</p>
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
                className="px-4 py-2 text-gray-700 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700 rounded-md font-medium"
              >
                Graph
              </Link>
              <Link
                href="/search"
                className="px-4 py-2 bg-blue-600 text-white rounded-md font-medium"
              >
                Search
              </Link>
            </nav>
          </div>
        </div>
      </header>

      {/* Main Content */}
      <main className="container mx-auto px-4 py-8">
        <div className="max-w-4xl mx-auto">
          {/* Search Form */}
          <div className="mb-8">
            <h1 className="text-3xl font-bold text-gray-900 dark:text-white mb-4">
              Search Code
            </h1>
            <form onSubmit={handleSearch} className="flex gap-2">
              <input
                type="text"
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                placeholder="Search for symbols, functions, classes..."
                className="flex-1 px-4 py-3 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
              <button
                type="submit"
                className="px-6 py-3 bg-blue-600 text-white rounded-lg hover:bg-blue-700 font-medium disabled:bg-gray-400 disabled:cursor-not-allowed"
                disabled={!searchQuery.trim()}
              >
                Search
              </button>
            </form>
          </div>

          {/* Loading State */}
          {loading && (
            <div className="flex items-center justify-center py-12">
              <div className="text-center">
                <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-blue-600 mx-auto mb-4"></div>
                <p className="text-gray-600 dark:text-gray-400">Searching...</p>
              </div>
            </div>
          )}

          {/* Error State */}
          {error && (
            <div className="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg p-6">
              <h3 className="text-lg font-semibold text-red-800 dark:text-red-300 mb-2">
                Error searching
              </h3>
              <p className="text-red-600 dark:text-red-400">{error.message}</p>
              <p className="text-sm text-red-500 dark:text-red-400 mt-2">
                Make sure the GraphQL server is running at http://localhost:8080/graphql
              </p>
            </div>
          )}

          {/* No Query */}
          {!submittedQuery && !loading && !error && (
            <div className="bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-lg p-12 text-center">
              <div className="text-6xl mb-4">🔍</div>
              <h3 className="text-xl font-semibold text-gray-900 dark:text-white mb-2">
                Search for code symbols
              </h3>
              <p className="text-gray-600 dark:text-gray-400">
                Enter a search query above to find functions, classes, variables, and more
              </p>
            </div>
          )}

          {/* Results */}
          {!loading && !error && data?.symbols && (
            <div>
              <div className="mb-4 text-gray-600 dark:text-gray-400">
                Found {data.symbols.length} {data.symbols.length === 1 ? 'result' : 'results'}
                {submittedQuery && ` for "${submittedQuery}"`}
              </div>

              {data.symbols.length === 0 ? (
                <div className="bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-lg p-12 text-center">
                  <div className="text-6xl mb-4">📭</div>
                  <h3 className="text-xl font-semibold text-gray-900 dark:text-white mb-2">
                    No results found
                  </h3>
                  <p className="text-gray-600 dark:text-gray-400">
                    Try adjusting your search query or indexing more code
                  </p>
                </div>
              ) : (
                <div className="space-y-4">
                  {data.symbols.map((symbol, index) => (
                    <div
                      key={`${symbol.scipSymbol}-${index}`}
                      className="bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-lg p-6 hover:shadow-lg transition-shadow"
                    >
                      {/* Symbol Header */}
                      <div className="flex items-start justify-between mb-3">
                        <div className="flex-1">
                          <div className="flex items-center gap-2 mb-2">
                            <h3 className="text-lg font-semibold text-gray-900 dark:text-white">
                              {symbol.displayName || symbol.name}
                            </h3>
                            <span className="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-purple-100 text-purple-800 dark:bg-purple-900 dark:text-purple-300">
                              {symbol.kind}
                            </span>
                          </div>
                          {symbol.signature && (
                            <code className="text-sm text-gray-700 dark:text-gray-300 bg-gray-50 dark:bg-gray-900 px-2 py-1 rounded">
                              {symbol.signature}
                            </code>
                          )}
                        </div>
                      </div>

                      {/* Symbol Documentation */}
                      {symbol.documentation && (
                        <p className="text-gray-600 dark:text-gray-400 mb-3 text-sm">
                          {symbol.documentation}
                        </p>
                      )}

                      {/* Symbol Location */}
                      <div className="flex items-center gap-4 text-sm text-gray-500 dark:text-gray-400">
                        <div className="flex items-center gap-1">
                          <span className="font-medium">File:</span>
                          <span className="font-mono text-xs">{symbol.filePath}</span>
                        </div>
                        {symbol.startLine && (
                          <div className="flex items-center gap-1">
                            <span className="font-medium">Line:</span>
                            <span>{symbol.startLine}</span>
                            {symbol.endLine && symbol.endLine !== symbol.startLine && (
                              <span>-{symbol.endLine}</span>
                            )}
                          </div>
                        )}
                        {symbol.serviceName && (
                          <div className="flex items-center gap-1">
                            <span className="font-medium">Service:</span>
                            <span>{symbol.serviceName}</span>
                          </div>
                        )}
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}
        </div>
      </main>
    </div>
  )
}
