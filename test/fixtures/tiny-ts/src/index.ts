import { createLogger, Logger } from "./logger";

function greet(logger: Logger, name: string): void {
  logger.log(`Hello, ${name}`);
}

const logger = createLogger("app");
greet(logger, "world");
