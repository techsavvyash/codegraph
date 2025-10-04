/**
 * User controller for handling user-related operations
 */

export class UserController {
  constructor(db, logger) {
    this.db = db;
    this.logger = logger;
  }

  /**
   * Create a new user
   * @param {Object} userData - User data
   * @param {string} userData.email - User email
   * @param {string} userData.name - User name
   * @returns {Promise<Object>} Created user
   */
  async createUser(userData) {
    this.logger.info(`Creating user: ${userData.email}`);

    const user = {
      id: this.generateId(),
      email: userData.email,
      name: userData.name,
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
   * @param {string} id - User ID
   * @returns {Promise<Object|null>} User or null if not found
   */
  async getUserById(id) {
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
   * Update user
   * @param {string} id - User ID
   * @param {Object} updates - Fields to update
   * @returns {Promise<Object>} Updated user
   */
  async updateUser(id, updates) {
    this.logger.info(`Updating user: ${id}`);

    const fields = [];
    const values = [];
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
   * Delete user
   * @param {string} id - User ID
   * @returns {Promise<void>}
   */
  async deleteUser(id) {
    this.logger.info(`Deleting user: ${id}`);
    await this.db.execute('DELETE FROM users WHERE id = $1', [id]);
  }

  /**
   * List users with pagination
   * @param {number} limit - Maximum number of users
   * @param {number} offset - Number of users to skip
   * @returns {Promise<Array>} List of users
   */
  async listUsers(limit = 10, offset = 0) {
    this.logger.debug(`Listing users: limit=${limit}, offset=${offset}`);

    const result = await this.db.query(
      'SELECT * FROM users ORDER BY created_at DESC LIMIT $1 OFFSET $2',
      [limit, offset]
    );

    return result.rows.map(row => this.mapRowToUser(row));
  }

  /**
   * Search users by email or name
   * @param {string} query - Search query
   * @returns {Promise<Array>} Matching users
   */
  async searchUsers(query) {
    this.logger.debug(`Searching users: ${query}`);

    const result = await this.db.query(
      'SELECT * FROM users WHERE email LIKE $1 OR name LIKE $1',
      [`%${query}%`]
    );

    return result.rows.map(row => this.mapRowToUser(row));
  }

  /**
   * Generate unique user ID
   * @returns {string} User ID
   */
  generateId() {
    const timestamp = Date.now();
    const random = Math.random().toString(36).substr(2, 9);
    return `user_${timestamp}_${random}`;
  }

  /**
   * Map database row to user object
   * @param {Object} row - Database row
   * @returns {Object} User object
   */
  mapRowToUser(row) {
    return {
      id: row.id,
      email: row.email,
      name: row.name,
      createdAt: new Date(row.created_at)
    };
  }
}
