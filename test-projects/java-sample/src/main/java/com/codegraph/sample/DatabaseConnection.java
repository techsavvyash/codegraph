package com.codegraph.sample;

import java.util.ArrayList;
import java.util.List;

/**
 * Database connection manager
 */
public class DatabaseConnection {
    private final String url;
    private boolean connected = false;

    /**
     * Create a new database connection
     *
     * @param url Database connection URL
     */
    public DatabaseConnection(String url) {
        this.url = url;
    }

    /**
     * Connect to the database
     */
    public void connect() {
        System.out.println("Connecting to database: " + url);
        try {
            Thread.sleep(100);
            connected = true;
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
    }

    /**
     * Disconnect from the database
     */
    public void disconnect() {
        System.out.println("Disconnecting from database");
        try {
            Thread.sleep(50);
            connected = false;
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
    }

    /**
     * Execute a query that returns results
     *
     * @param sql SQL query
     * @param params Query parameters
     * @return Query results
     * @throws IllegalStateException If database is not connected
     */
    public List<User> query(String sql, Object... params) {
        ensureConnected();
        System.out.println("Executing query: " + sql);
        try {
            Thread.sleep(10);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
        return new ArrayList<>();
    }

    /**
     * Execute a query that doesn't return results
     *
     * @param sql SQL query
     * @param params Query parameters
     * @throws IllegalStateException If database is not connected
     */
    public void execute(String sql, Object... params) {
        ensureConnected();
        System.out.println("Executing statement: " + sql);
        try {
            Thread.sleep(10);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
    }

    /**
     * Check if connected to database
     *
     * @return True if connected, false otherwise
     */
    public boolean isConnected() {
        return connected;
    }

    /**
     * Ensure database is connected
     *
     * @throws IllegalStateException If database is not connected
     */
    private void ensureConnected() {
        if (!connected) {
            throw new IllegalStateException("Database not connected");
        }
    }
}
