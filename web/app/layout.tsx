import type { Metadata } from 'next'
import './globals.css'
import { ApolloWrapper } from '@/lib/apollo-wrapper'

export const metadata: Metadata = {
  title: 'CodeGraph - Code Visualization Dashboard',
  description: 'Interactive code graph visualization and exploration tool',
}

export default function RootLayout({
  children,
}: {
  children: React.ReactNode
}) {
  return (
    <html lang="en">
      <body>
        <ApolloWrapper>
          {children}
        </ApolloWrapper>
      </body>
    </html>
  )
}
