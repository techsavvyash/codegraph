"""
Logger utility module
"""

from enum import Enum
from datetime import datetime
from typing import Any


class LogLevel(Enum):
    """Log level enumeration"""
    DEBUG = 0
    INFO = 1
    WARN = 2
    ERROR = 3


class Logger:
    """Simple logger utility"""

    def __init__(self, level: LogLevel = LogLevel.INFO):
        """
        Initialize logger

        Args:
            level: Minimum log level to display
        """
        self.level = level

    def debug(self, message: str, *args: Any) -> None:
        """
        Log debug message

        Args:
            message: Log message
            *args: Additional arguments
        """
        self._log(LogLevel.DEBUG, message, *args)

    def info(self, message: str, *args: Any) -> None:
        """
        Log info message

        Args:
            message: Log message
            *args: Additional arguments
        """
        self._log(LogLevel.INFO, message, *args)

    def warn(self, message: str, *args: Any) -> None:
        """
        Log warning message

        Args:
            message: Log message
            *args: Additional arguments
        """
        self._log(LogLevel.WARN, message, *args)

    def error(self, message: str, *args: Any) -> None:
        """
        Log error message

        Args:
            message: Log message
            *args: Additional arguments
        """
        self._log(LogLevel.ERROR, message, *args)

    def set_level(self, level: LogLevel) -> None:
        """
        Set log level

        Args:
            level: New log level
        """
        self.level = level

    def get_level(self) -> LogLevel:
        """
        Get current log level

        Returns:
            Current log level
        """
        return self.level

    def _log(self, level: LogLevel, message: str, *args: Any) -> None:
        """
        Internal log method

        Args:
            level: Log level
            message: Log message
            *args: Additional arguments
        """
        if level.value >= self.level.value:
            timestamp = datetime.now().isoformat()
            formatted_message = f'[{timestamp}] [{level.name}] {message}'

            if args:
                formatted_message += f' {args}'

            print(formatted_message)
