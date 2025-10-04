package com.codegraph.sample;

/**
 * Main application class
 */
public class App {
    private final UserService userService;
    private final DatabaseConnection db;
    private final Logger logger;

    /**
     * Create a new Application
     *
     * @param databaseUrl Database connection URL
     * @param logLevel Log level
     */
    public App(String databaseUrl, Logger.LogLevel logLevel) {
        this.logger = new Logger(logLevel);
        this.db = new DatabaseConnection(databaseUrl);
        this.userService = new UserService(db, logger);
    }

    /**
     * Initialize the application
     */
    public void initialize() {
        logger.info("Initializing application...");
        db.connect();
        logger.info("Database connected");
    }

    /**
     * Start the application
     */
    public void start() {
        initialize();
        int port = 8000;
        logger.info("Application started on port " + port);
    }

    /**
     * Shutdown the application gracefully
     */
    public void shutdown() {
        logger.info("Shutting down application...");
        db.disconnect();
        logger.info("Application shutdown complete");
    }

    /**
     * Get the user service instance
     *
     * @return User service instance
     */
    public UserService getUserService() {
        return userService;
    }

    /**
     * Main entry point
     *
     * @param args Command line arguments
     */
    public static void main(String[] args) {
        App app = new App("postgresql://localhost:5432/mydb", Logger.LogLevel.INFO);

        try {
            app.start();

            // Demonstrate user service functionality
            User user = app.getUserService().createUser("test@example.com", "Test User");
            System.out.println("Created user: " + user.getId());

            // List users
            var users = app.getUserService().listUsers(10, 0);
            System.out.println("Found " + users.size() + " users");

        } catch (Exception e) {
            System.err.println("Fatal error: " + e.getMessage());
            System.exit(1);
        } finally {
            app.shutdown();
        }
    }
}
