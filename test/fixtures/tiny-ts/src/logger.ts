export interface Logger {
  log(message: string): void;
}

export class ConsoleLogger implements Logger {
  constructor(private readonly prefix: string) {}

  log(message: string): void {
    console.log(`[${this.prefix}] ${message}`);
  }
}

export function createLogger(prefix: string): Logger {
  return new ConsoleLogger(prefix);
}
