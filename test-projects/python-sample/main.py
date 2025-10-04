"""
Main entry point for the Python sample application
"""

import asyncio
import sys
from datetime import datetime
from typing import Optional

from database.connection import DatabaseConnection
from services.user_service import UserService
from utils.logger import Logger, LogLevel


class Application:
    """Main application class"""

    def __init__(self, config: dict):
        """
        Initialize the application

        Args:
            config: Application configuration dictionary
        """
        self.config = config
        self.logger = Logger(LogLevel(config.get('log_level', 'info')))
        self.db = DatabaseConnection(config['database_url'])
        self.user_service = UserService(self.db, self.logger)

    async def initialize(self) -> None:
        """Initialize the application components"""
        self.logger.info('Initializing application...')
        await self.db.connect()
        self.logger.info('Database connected')

    async def start(self) -> None:
        """Start the application"""
        await self.initialize()
        port = self.config.get('port', 8000)
        self.logger.info(f'Application started on port {port}')

    async def shutdown(self) -> None:
        """Shutdown the application gracefully"""
        self.logger.info('Shutting down application...')
        await self.db.disconnect()
        self.logger.info('Application shutdown complete')


async def main() -> None:
    """
    Create and start the application
    """
    config = {
        'port': 8000,
        'database_url': 'postgresql://localhost:5432/mydb',
        'log_level': 'info'
    }

    app = Application(config)

    try:
        await app.start()

        # Demonstrate user service functionality
        user = await app.user_service.create_user(
            email='test@example.com',
            name='Test User'
        )
        app.logger.info(f'Created user: {user.id}')

        # List users
        users = await app.user_service.list_users(limit=10)
        app.logger.info(f'Found {len(users)} users')

    except KeyboardInterrupt:
        app.logger.info('Received interrupt signal')
    except Exception as e:
        app.logger.error(f'Fatal error: {e}')
        sys.exit(1)
    finally:
        await app.shutdown()


if __name__ == '__main__':
    asyncio.run(main())
