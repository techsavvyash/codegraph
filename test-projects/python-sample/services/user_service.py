"""
User service module for managing user operations
"""

from dataclasses import dataclass
from datetime import datetime
from typing import List, Optional
import time
import random
import string

from database.connection import DatabaseConnection
from utils.logger import Logger


@dataclass
class User:
    """User data model"""
    id: str
    email: str
    name: str
    created_at: datetime


class UserService:
    """Service for managing user operations"""

    def __init__(self, db: DatabaseConnection, logger: Logger):
        """
        Initialize the UserService

        Args:
            db: Database connection instance
            logger: Logger instance
        """
        self.db = db
        self.logger = logger

    async def create_user(self, email: str, name: str) -> User:
        """
        Create a new user

        Args:
            email: User's email address
            name: User's full name

        Returns:
            The created user

        Raises:
            ValueError: If email or name is invalid
        """
        self.logger.info(f'Creating user: {email}')

        if not email or '@' not in email:
            raise ValueError('Invalid email address')

        if not name or len(name) < 2:
            raise ValueError('Invalid name')

        user = User(
            id=self._generate_id(),
            email=email,
            name=name,
            created_at=datetime.now()
        )

        await self.db.execute(
            'INSERT INTO users (id, email, name, created_at) VALUES (?, ?, ?, ?)',
            (user.id, user.email, user.name, user.created_at)
        )

        self.logger.info(f'User created: {user.id}')
        return user

    async def get_user_by_id(self, user_id: str) -> Optional[User]:
        """
        Get user by ID

        Args:
            user_id: User ID

        Returns:
            User if found, None otherwise
        """
        self.logger.debug(f'Fetching user: {user_id}')

        result = await self.db.query(
            'SELECT * FROM users WHERE id = ?',
            (user_id,)
        )

        if not result.rows:
            return None

        return self._map_row_to_user(result.rows[0])

    async def update_user(self, user_id: str, email: Optional[str] = None,
                         name: Optional[str] = None) -> User:
        """
        Update user information

        Args:
            user_id: User ID
            email: New email address (optional)
            name: New name (optional)

        Returns:
            Updated user

        Raises:
            ValueError: If user not found
        """
        self.logger.info(f'Updating user: {user_id}')

        fields = []
        values = []

        if email:
            fields.append('email = ?')
            values.append(email)

        if name:
            fields.append('name = ?')
            values.append(name)

        if not fields:
            raise ValueError('No fields to update')

        values.append(user_id)

        await self.db.execute(
            f'UPDATE users SET {", ".join(fields)} WHERE id = ?',
            tuple(values)
        )

        updated_user = await self.get_user_by_id(user_id)
        if not updated_user:
            raise ValueError(f'User not found: {user_id}')

        return updated_user

    async def delete_user(self, user_id: str) -> None:
        """
        Delete a user

        Args:
            user_id: User ID to delete
        """
        self.logger.info(f'Deleting user: {user_id}')
        await self.db.execute('DELETE FROM users WHERE id = ?', (user_id,))

    async def list_users(self, limit: int = 10, offset: int = 0) -> List[User]:
        """
        List all users with pagination

        Args:
            limit: Maximum number of users to return
            offset: Number of users to skip

        Returns:
            List of users
        """
        self.logger.debug(f'Listing users: limit={limit}, offset={offset}')

        result = await self.db.query(
            'SELECT * FROM users ORDER BY created_at DESC LIMIT ? OFFSET ?',
            (limit, offset)
        )

        return [self._map_row_to_user(row) for row in result.rows]

    async def search_users(self, query: str) -> List[User]:
        """
        Search users by email or name

        Args:
            query: Search query

        Returns:
            List of matching users
        """
        self.logger.debug(f'Searching users: {query}')

        result = await self.db.query(
            'SELECT * FROM users WHERE email LIKE ? OR name LIKE ?',
            (f'%{query}%', f'%{query}%')
        )

        return [self._map_row_to_user(row) for row in result.rows]

    def _generate_id(self) -> str:
        """Generate a unique user ID"""
        timestamp = int(time.time() * 1000)
        random_str = ''.join(random.choices(string.ascii_lowercase + string.digits, k=9))
        return f'user_{timestamp}_{random_str}'

    def _map_row_to_user(self, row: dict) -> User:
        """Map database row to User object"""
        return User(
            id=row['id'],
            email=row['email'],
            name=row['name'],
            created_at=datetime.fromisoformat(row['created_at'])
            if isinstance(row['created_at'], str)
            else row['created_at']
        )
