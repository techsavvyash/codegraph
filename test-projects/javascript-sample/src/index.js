/**
 * Main entry point for the JavaScript sample application
 */

import { UserController } from './controllers/UserController.js';
import { DatabaseService } from './services/DatabaseService.js';
import { Logger } from './utils/Logger.js';

/**
 * Main application class
 */
export class Application {
  constructor(config) {
    this.config = config;
    this.logger = new Logger(config.logLevel || 'info');
    this.db = new DatabaseService(config.databaseUrl);
    this.userController = new UserController(this.db, this.logger);
  }

  /**
   * Initialize the application
   */
  async initialize() {
    this.logger.info('Initializing application...');
    await this.db.connect();
    this.logger.info('Database connected');
  }

  /**
   * Start the application
   */
  async start() {
    await this.initialize();
    this.logger.info(`Application started on port ${this.config.port}`);

    // Setup routes
    this.setupRoutes();
  }

  /**
   * Setup application routes
   */
  setupRoutes() {
    this.logger.debug('Setting up routes...');
    // Route setup would go here
  }

  /**
   * Shutdown the application gracefully
   */
  async shutdown() {
    this.logger.info('Shutting down application...');
    await this.db.disconnect();
    this.logger.info('Application shutdown complete');
  }
}

/**
 * Create and start the application
 */
async function main() {
  const config = {
    port: 3000,
    databaseUrl: 'postgresql://localhost:5432/mydb',
    logLevel: 'info'
  };

  const app = new Application(config);

  // Handle graceful shutdown
  process.on('SIGINT', async () => {
    await app.shutdown();
    process.exit(0);
  });

  try {
    await app.start();
  } catch (error) {
    console.error('Fatal error:', error);
    process.exit(1);
  }
}

// Run the application if this is the main module
if (import.meta.url === `file://${process.argv[1]}`) {
  main();
}
