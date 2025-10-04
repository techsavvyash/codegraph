<?php

namespace CodeGraph\Sample;

/**
 * Log level enumeration
 */
enum LogLevel: int
{
    case DEBUG = 0;
    case INFO = 1;
    case WARN = 2;
    case ERROR = 3;
}

/**
 * Simple logger utility
 */
class Logger
{
    private LogLevel $level;

    /**
     * Create a new logger
     *
     * @param LogLevel $level Minimum log level to display
     */
    public function __construct(LogLevel $level = LogLevel::INFO)
    {
        $this->level = $level;
    }

    /**
     * Log debug message
     *
     * @param string $message Log message
     * @param mixed ...$args Additional arguments
     */
    public function debug(string $message, mixed ...$args): void
    {
        $this->log(LogLevel::DEBUG, $message, ...$args);
    }

    /**
     * Log info message
     *
     * @param string $message Log message
     * @param mixed ...$args Additional arguments
     */
    public function info(string $message, mixed ...$args): void
    {
        $this->log(LogLevel::INFO, $message, ...$args);
    }

    /**
     * Log warning message
     *
     * @param string $message Log message
     * @param mixed ...$args Additional arguments
     */
    public function warn(string $message, mixed ...$args): void
    {
        $this->log(LogLevel::WARN, $message, ...$args);
    }

    /**
     * Log error message
     *
     * @param string $message Log message
     * @param mixed ...$args Additional arguments
     */
    public function error(string $message, mixed ...$args): void
    {
        $this->log(LogLevel::ERROR, $message, ...$args);
    }

    /**
     * Set log level
     *
     * @param LogLevel $level New log level
     */
    public function setLevel(LogLevel $level): void
    {
        $this->level = $level;
    }

    /**
     * Get current log level
     *
     * @return LogLevel Current log level
     */
    public function getLevel(): LogLevel
    {
        return $this->level;
    }

    /**
     * Internal log method
     *
     * @param LogLevel $level Log level
     * @param string $message Log message
     * @param mixed ...$args Additional arguments
     */
    private function log(LogLevel $level, string $message, mixed ...$args): void
    {
        if ($level->value >= $this->level->value) {
            $timestamp = date('Y-m-d H:i:s');
            $levelName = $level->name;
            $formattedMessage = "[{$timestamp}] [{$levelName}] {$message}";

            if (!empty($args)) {
                $formattedMessage .= ' ' . json_encode($args);
            }

            echo $formattedMessage . "\n";
        }
    }
}
