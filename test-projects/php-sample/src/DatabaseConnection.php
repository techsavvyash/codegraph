<?php

namespace CodeGraph\Sample;

/**
 * Database connection manager
 */
class DatabaseConnection
{
    private string $url;
    private bool $connected = false;

    /**
     * Create a new database connection
     *
     * @param string $url Database connection URL
     */
    public function __construct(string $url)
    {
        $this->url = $url;
    }

    /**
     * Connect to the database
     */
    public function connect(): void
    {
        echo "Connecting to database: {$this->url}\n";
        // Simulate async connection
        usleep(100000);
        $this->connected = true;
    }

    /**
     * Disconnect from the database
     */
    public function disconnect(): void
    {
        echo "Disconnecting from database\n";
        usleep(50000);
        $this->connected = false;
    }

    /**
     * Execute a query that returns results
     *
     * @param string $sql SQL query
     * @param array $params Query parameters
     * @return array Query results
     * @throws \RuntimeException If database is not connected
     */
    public function query(string $sql, array $params = []): array
    {
        $this->ensureConnected();
        echo "Executing query: {$sql}\n";
        usleep(10000);
        return ['rows' => [], 'rowCount' => 0];
    }

    /**
     * Execute a query that doesn't return results
     *
     * @param string $sql SQL query
     * @param array $params Query parameters
     * @throws \RuntimeException If database is not connected
     */
    public function execute(string $sql, array $params = []): void
    {
        $this->ensureConnected();
        echo "Executing statement: {$sql}\n";
        usleep(10000);
    }

    /**
     * Check if connected to database
     *
     * @return bool True if connected, false otherwise
     */
    public function isConnected(): bool
    {
        return $this->connected;
    }

    /**
     * Ensure database is connected
     *
     * @throws \RuntimeException If database is not connected
     */
    private function ensureConnected(): void
    {
        if (!$this->connected) {
            throw new \RuntimeException('Database not connected');
        }
    }
}
