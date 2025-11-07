import Link from 'next/link'

export default function Home() {
  return (
    <main className="min-h-screen bg-gradient-to-b from-gray-50 to-gray-100 dark:from-gray-900 dark:to-gray-800">
      <div className="container mx-auto px-4 py-16">
        {/* Header */}
        <div className="text-center mb-16">
          <h1 className="text-5xl font-bold mb-4 bg-gradient-to-r from-blue-600 to-purple-600 bg-clip-text text-transparent">
            CodeGraph
          </h1>
          <p className="text-xl text-gray-600 dark:text-gray-300">
            Interactive Code Visualization & Exploration
          </p>
        </div>

        {/* Feature Cards */}
        <div className="grid md:grid-cols-2 lg:grid-cols-3 gap-6 max-w-6xl mx-auto mb-12">
          <Link href="/dashboard" className="group">
            <div className="p-6 bg-white dark:bg-gray-800 rounded-lg shadow-lg hover:shadow-xl transition-shadow border border-gray-200 dark:border-gray-700">
              <div className="text-4xl mb-4">📊</div>
              <h3 className="text-xl font-semibold mb-2 group-hover:text-blue-600 dark:group-hover:text-blue-400 transition-colors">
                Dashboard
              </h3>
              <p className="text-gray-600 dark:text-gray-400">
                View all indexed services and their statistics
              </p>
            </div>
          </Link>

          <Link href="/graph" className="group">
            <div className="p-6 bg-white dark:bg-gray-800 rounded-lg shadow-lg hover:shadow-xl transition-shadow border border-gray-200 dark:border-gray-700">
              <div className="text-4xl mb-4">🕸️</div>
              <h3 className="text-xl font-semibold mb-2 group-hover:text-blue-600 dark:group-hover:text-blue-400 transition-colors">
                Code Graph
              </h3>
              <p className="text-gray-600 dark:text-gray-400">
                Explore code relationships with interactive graphs
              </p>
            </div>
          </Link>

          <Link href="/search" className="group">
            <div className="p-6 bg-white dark:bg-gray-800 rounded-lg shadow-lg hover:shadow-xl transition-shadow border border-gray-200 dark:border-gray-700">
              <div className="text-4xl mb-4">🔍</div>
              <h3 className="text-xl font-semibold mb-2 group-hover:text-blue-600 dark:group-hover:text-blue-400 transition-colors">
                Search
              </h3>
              <p className="text-gray-600 dark:text-gray-400">
                Find symbols, files, and code across services
              </p>
            </div>
          </Link>
        </div>

        {/* Quick Start */}
        <div className="max-w-2xl mx-auto bg-white dark:bg-gray-800 rounded-lg shadow-lg p-8 border border-gray-200 dark:border-gray-700">
          <h2 className="text-2xl font-semibold mb-4">Quick Start</h2>
          <div className="space-y-3 text-gray-600 dark:text-gray-300">
            <div className="flex items-start">
              <span className="inline-block w-6 h-6 bg-blue-500 text-white rounded-full text-center mr-3 flex-shrink-0">1</span>
              <p>Make sure the GraphQL server is running at <code className="bg-gray-100 dark:bg-gray-700 px-2 py-1 rounded">http://localhost:8080/graphql</code></p>
            </div>
            <div className="flex items-start">
              <span className="inline-block w-6 h-6 bg-blue-500 text-white rounded-full text-center mr-3 flex-shrink-0">2</span>
              <p>Index your codebase using the CLI: <code className="bg-gray-100 dark:bg-gray-700 px-2 py-1 rounded">make index-self-scip</code></p>
            </div>
            <div className="flex items-start">
              <span className="inline-block w-6 h-6 bg-blue-500 text-white rounded-full text-center mr-3 flex-shrink-0">3</span>
              <p>Start exploring your code through the dashboard</p>
            </div>
          </div>
        </div>
      </div>
    </main>
  )
}
