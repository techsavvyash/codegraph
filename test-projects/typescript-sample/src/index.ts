/**
 * Main entry point for the TypeScript sample application
 */

import { UserService } from './services/UserService';
import { DatabaseConnection } from './database/DatabaseConnection';
import { Logger } from './utils/Logger';

/**
 * Application configuration interface
 */
interface AppConfig {
  port: number;
  databaseUrl: string;
  logLevel: 'debug' | 'info' | 'warn' | 'error';
}

/**
 * Main application class
 */
export class Application {
  private userService: UserService;
  private db: DatabaseConnection;
  private logger: Logger;
  private config: AppConfig;

  constructor(config: AppConfig) {
    this.config = config;
    this.logger = new Logger(config.logLevel);
    this.db = new DatabaseConnection(config.databaseUrl);
    this.userService = new UserService(this.db, this.logger);
  }

  /**
   * Initialize the application
   */
  async initialize(): Promise<void> {
    this.logger.info('Initializing application...');
    await this.db.connect();
    this.logger.info('Database connected');
  }

  /**
   * Start the application
   */
  async start(): Promise<void> {
    await this.initialize();
    this.logger.info(`Application started on port ${this.config.port}`);
  }

  /**
   * Shutdown the application gracefully
   */
  async shutdown(): Promise<void> {
    this.logger.info('Shutting down application...');
    await this.db.disconnect();
    this.logger.info('Application shutdown complete');
  }
}

/**
 * Create and start the application
 */
async function main() {
  const config: AppConfig = {
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

  await app.start();
}

// Run the application if this is the main module
if (require.main === module) {
  main().catch((error) => {
    console.error('Fatal error:', error);
    process.exit(1);
  });
}

export { AppConfig };
