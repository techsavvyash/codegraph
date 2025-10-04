package com.codegraph.sample;

import java.time.LocalDateTime;
import java.time.format.DateTimeFormatter;

/**
 * Simple logger utility
 */
public class Logger {
    /**
     * Log level enumeration
     */
    public enum LogLevel {
        DEBUG(0),
        INFO(1),
        WARN(2),
        ERROR(3);

        private final int value;

        LogLevel(int value) {
            this.value = value;
        }

        public int getValue() {
            return value;
        }
    }

    private LogLevel level;
    private static final DateTimeFormatter formatter = DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm:ss");

    /**
     * Create a new logger
     *
     * @param level Minimum log level to display
     */
    public Logger(LogLevel level) {
        this.level = level;
    }

    /**
     * Log debug message
     *
     * @param message Log message
     */
    public void debug(String message) {
        log(LogLevel.DEBUG, message);
    }

    /**
     * Log info message
     *
     * @param message Log message
     */
    public void info(String message) {
        log(LogLevel.INFO, message);
    }

    /**
     * Log warning message
     *
     * @param message Log message
     */
    public void warn(String message) {
        log(LogLevel.WARN, message);
    }

    /**
     * Log error message
     *
     * @param message Log message
     */
    public void error(String message) {
        log(LogLevel.ERROR, message);
    }

    /**
     * Set log level
     *
     * @param level New log level
     */
    public void setLevel(LogLevel level) {
        this.level = level;
    }

    /**
     * Get current log level
     *
     * @return Current log level
     */
    public LogLevel getLevel() {
        return level;
    }

    /**
     * Internal log method
     *
     * @param level Log level
     * @param message Log message
     */
    private void log(LogLevel level, String message) {
        if (level.getValue() >= this.level.getValue()) {
            String timestamp = LocalDateTime.now().format(formatter);
            String levelName = level.name();
            String formattedMessage = String.format("[%s] [%s] %s", timestamp, levelName, message);
            System.out.println(formattedMessage);
        }
    }
}
