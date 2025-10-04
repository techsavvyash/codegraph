"""
Database connection manager module
"""

import asyncio
from dataclasses import dataclass
from typing import Any, List, Tuple, Optional


@dataclass
class QueryResult:
    """Database query result"""
    rows: List[dict]
    row_count: int


class DatabaseConnection:
    """Database connection manager"""

    def __init__(self, url: str):
        """
        Initialize database connection

        Args:
            url: Database connection URL
        """
        self.url = url
        self.connected = False

    async def connect(self) -> None:
        """Connect to the database"""
        print(f'Connecting to database: {self.url}')
        await asyncio.sleep(0.1)  # Simulate async connection
        self.connected = True

    async def disconnect(self) -> None:
        """Disconnect from the database"""
        print('Disconnecting from database')
        await asyncio.sleep(0.05)
        self.connected = False

    async def query(self, sql: str, params: Optional[Tuple] = None) -> QueryResult:
        """
        Execute a query that returns results

        Args:
            sql: SQL query string
            params: Query parameters tuple

        Returns:
            Query results

        Raises:
            RuntimeError: If database is not connected
        """
        self._ensure_connected()
        print(f'Executing query: {sql}')
        await asyncio.sleep(0.01)  # Simulate async query
        return QueryResult(rows=[], row_count=0)

    async def execute(self, sql: str, params: Optional[Tuple] = None) -> None:
        """
        Execute a query that doesn't return results

        Args:
            sql: SQL query string
            params: Query parameters tuple

        Raises:
            RuntimeError: If database is not connected
        """
        self._ensure_connected()
        print(f'Executing statement: {sql}')
        await asyncio.sleep(0.01)

    def is_connected(self) -> bool:
        """
        Check if connected to database

        Returns:
            True if connected, False otherwise
        """
        return self.connected

    def _ensure_connected(self) -> None:
        """
        Ensure database is connected

        Raises:
            RuntimeError: If database is not connected
        """
        if not self.connected:
            raise RuntimeError('Database not connected')
