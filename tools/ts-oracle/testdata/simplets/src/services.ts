export class Logger {
  log(message: string): void {
    // eslint-disable-next-line no-console
    console.log(message);
  }
}

export class Store {
  private logger = new Logger();

  save(value: string): void {
    this.logger.log(`saving ${value}`);
  }

  saveAll(values: string[]): void {
    // Class method calling another class's method (this.logger is a
    // different class instance).
    for (const v of values) {
      this.logger.log(v);
    }
  }
}

export class Consumer {
  private store = new Store();

  run(value: string): void {
    // Class method calling a method on another class instance.
    this.store.save(value);
  }
}
