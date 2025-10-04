import { DatabaseConnection } from '../database/DatabaseConnection';
import { Logger } from '../utils/Logger';

/**
 * User data model
 */
export interface User {
  id: string;
  email: string;
  name: string;
  createdAt: Date;
}

/**
 * Service for managing user operations
 */
export class UserService {
  private db: DatabaseConnection;
  private logger: Logger;

  constructor(db: DatabaseConnection, logger: Logger) {
    this.db = db;
    this.logger = logger;
  }

  /**
   * Create a new user
   * @param email - User's email address
   * @param name - User's full name
   * @returns The created user
   */
  async createUser(email: string, name: string): Promise<User> {
    this.logger.info(`Creating user: ${email}`);

    const user: User = {
      id: this.generateId(),
      email,
      name,
      createdAt: new Date()
    };

    await this.db.execute(
      'INSERT INTO users (id, email, name, created_at) VALUES ($1, $2, $3, $4)',
      [user.id, user.email, user.name, user.createdAt]
    );

    this.logger.info(`User created: ${user.id}`);
    return user;
  }

  /**
   * Get user by ID
   * @param id - User ID
   * @returns User if found, null otherwise
   */
  async getUserById(id: string): Promise<User | null> {
    this.logger.debug(`Fetching user: ${id}`);

    const result = await this.db.query(
      'SELECT * FROM users WHERE id = $1',
      [id]
    );

    if (result.rows.length === 0) {
      return null;
    }

    return this.mapRowToUser(result.rows[0]);
  }

  /**
   * Update user information
   * @param id - User ID
   * @param updates - Fields to update
   * @returns Updated user
   */
  async updateUser(id: string, updates: Partial<User>): Promise<User> {
    this.logger.info(`Updating user: ${id}`);

    const fields: string[] = [];
    const values: any[] = [];
    let paramIndex = 1;

    if (updates.email) {
      fields.push(`email = $${paramIndex++}`);
      values.push(updates.email);
    }

    if (updates.name) {
      fields.push(`name = $${paramIndex++}`);
      values.push(updates.name);
    }

    values.push(id);

    await this.db.execute(
      `UPDATE users SET ${fields.join(', ')} WHERE id = $${paramIndex}`,
      values
    );

    const updatedUser = await this.getUserById(id);
    if (!updatedUser) {
      throw new Error(`User not found: ${id}`);
    }

    return updatedUser;
  }

  /**
   * Delete a user
   * @param id - User ID to delete
   */
  async deleteUser(id: string): Promise<void> {
    this.logger.info(`Deleting user: ${id}`);
    await this.db.execute('DELETE FROM users WHERE id = $1', [id]);
  }

  /**
   * List all users with pagination
   * @param limit - Maximum number of users to return
   * @param offset - Number of users to skip
   * @returns List of users
   */
  async listUsers(limit: number = 10, offset: number = 0): Promise<User[]> {
    this.logger.debug(`Listing users: limit=${limit}, offset=${offset}`);

    const result = await this.db.query(
      'SELECT * FROM users ORDER BY created_at DESC LIMIT $1 OFFSET $2',
      [limit, offset]
    );

    return result.rows.map(row => this.mapRowToUser(row));
  }

  /**
   * Generate a unique user ID
   */
  private generateId(): string {
    return `user_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`;
  }

  /**
   * Map database row to User object
   */
  private mapRowToUser(row: any): User {
    return {
      id: row.id,
      email: row.email,
      name: row.name,
      createdAt: new Date(row.created_at)
    };
  }
}
