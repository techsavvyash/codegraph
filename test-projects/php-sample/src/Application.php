<?php

namespace CodeGraph\Sample;

/**
 * Main application class
 */
class Application
{
    private UserService $userService;
    private DatabaseConnection $db;
    private Logger $logger;
    private array $config;

    /**
     * Create a new Application
     *
     * @param array $config Application configuration
     */
    public function __construct(array $config)
    {
        $this->config = $config;
        $this->logger = new Logger($config['logLevel'] ?? LogLevel::INFO);
        $this->db = new DatabaseConnection($config['databaseUrl']);
        $this->userService = new UserService($this->db, $this->logger);
    }

    /**
     * Initialize the application
     */
    public function initialize(): void
    {
        $this->logger->info('Initializing application...');
        $this->db->connect();
        $this->logger->info('Database connected');
    }

    /**
     * Start the application
     */
    public function start(): void
    {
        $this->initialize();
        $port = $this->config['port'] ?? 8000;
        $this->logger->info("Application started on port {$port}");
    }

    /**
     * Shutdown the application gracefully
     */
    public function shutdown(): void
    {
        $this->logger->info('Shutting down application...');
        $this->db->disconnect();
        $this->logger->info('Application shutdown complete');
    }

    /**
     * Get the user service instance
     *
     * @return UserService User service instance
     */
    public function getUserService(): UserService
    {
        return $this->userService;
    }
}

// Main entry point
if (php_sapi_name() === 'cli') {
    $config = [
        'port' => 8000,
        'databaseUrl' => 'postgresql://localhost:5432/mydb',
        'logLevel' => LogLevel::INFO
    ];

    $app = new Application($config);

    try {
        $app->start();

        // Demonstrate user service functionality
        $user = $app->getUserService()->createUser('test@example.com', 'Test User');
        echo "Created user: {$user->id}\n";

        // List users
        $users = $app->getUserService()->listUsers(10);
        echo "Found " . count($users) . " users\n";

    } catch (\Exception $e) {
        echo "Fatal error: {$e->getMessage()}\n";
        exit(1);
    } finally {
        $app->shutdown();
    }
}
