<?php

namespace CodeGraph\Sample;

use DateTime;

/**
 * User data model
 */
class User
{
    public function __construct(
        public string $id,
        public string $email,
        public string $name,
        public DateTime $createdAt
    ) {}
}

/**
 * Service for managing user operations
 */
class UserService
{
    private DatabaseConnection $db;
    private Logger $logger;

    /**
     * Create a new UserService
     *
     * @param DatabaseConnection $db Database connection instance
     * @param Logger $logger Logger instance
     */
    public function __construct(DatabaseConnection $db, Logger $logger)
    {
        $this->db = $db;
        $this->logger = $logger;
    }

    /**
     * Create a new user
     *
     * @param string $email User's email address
     * @param string $name User's full name
     * @return User The created user
     * @throws \Exception If email or name is invalid
     */
    public function createUser(string $email, string $name): User
    {
        $this->logger->info("Creating user: {$email}");

        if (empty($email) || !filter_var($email, FILTER_VALIDATE_EMAIL)) {
            throw new \Exception('Invalid email address');
        }

        if (empty($name) || strlen($name) < 2) {
            throw new \Exception('Invalid name');
        }

        $user = new User(
            id: $this->generateId(),
            email: $email,
            name: $name,
            createdAt: new DateTime()
        );

        $this->db->execute(
            'INSERT INTO users (id, email, name, created_at) VALUES (?, ?, ?, ?)',
            [$user->id, $user->email, $user->name, $user->createdAt->format('Y-m-d H:i:s')]
        );

        $this->logger->info("User created: {$user->id}");
        return $user;
    }

    /**
     * Get user by ID
     *
     * @param string $userId User ID
     * @return User|null User if found, null otherwise
     */
    public function getUserById(string $userId): ?User
    {
        $this->logger->debug("Fetching user: {$userId}");

        $result = $this->db->query(
            'SELECT * FROM users WHERE id = ?',
            [$userId]
        );

        if (empty($result['rows'])) {
            return null;
        }

        return $this->mapRowToUser($result['rows'][0]);
    }

    /**
     * Update user information
     *
     * @param string $userId User ID
     * @param string|null $email New email address (optional)
     * @param string|null $name New name (optional)
     * @return User Updated user
     * @throws \Exception If user not found or no fields to update
     */
    public function updateUser(string $userId, ?string $email = null, ?string $name = null): User
    {
        $this->logger->info("Updating user: {$userId}");

        $fields = [];
        $values = [];

        if ($email !== null) {
            $fields[] = 'email = ?';
            $values[] = $email;
        }

        if ($name !== null) {
            $fields[] = 'name = ?';
            $values[] = $name;
        }

        if (empty($fields)) {
            throw new \Exception('No fields to update');
        }

        $values[] = $userId;

        $this->db->execute(
            'UPDATE users SET ' . implode(', ', $fields) . ' WHERE id = ?',
            $values
        );

        $updatedUser = $this->getUserById($userId);
        if ($updatedUser === null) {
            throw new \Exception("User not found: {$userId}");
        }

        return $updatedUser;
    }

    /**
     * Delete a user
     *
     * @param string $userId User ID to delete
     */
    public function deleteUser(string $userId): void
    {
        $this->logger->info("Deleting user: {$userId}");
        $this->db->execute('DELETE FROM users WHERE id = ?', [$userId]);
    }

    /**
     * List all users with pagination
     *
     * @param int $limit Maximum number of users to return
     * @param int $offset Number of users to skip
     * @return array<User> List of users
     */
    public function listUsers(int $limit = 10, int $offset = 0): array
    {
        $this->logger->debug("Listing users: limit={$limit}, offset={$offset}");

        $result = $this->db->query(
            'SELECT * FROM users ORDER BY created_at DESC LIMIT ? OFFSET ?',
            [$limit, $offset]
        );

        return array_map(
            fn($row) => $this->mapRowToUser($row),
            $result['rows']
        );
    }

    /**
     * Search users by email or name
     *
     * @param string $query Search query
     * @return array<User> List of matching users
     */
    public function searchUsers(string $query): array
    {
        $this->logger->debug("Searching users: {$query}");

        $searchPattern = "%{$query}%";
        $result = $this->db->query(
            'SELECT * FROM users WHERE email LIKE ? OR name LIKE ?',
            [$searchPattern, $searchPattern]
        );

        return array_map(
            fn($row) => $this->mapRowToUser($row),
            $result['rows']
        );
    }

    /**
     * Generate a unique user ID
     *
     * @return string User ID
     */
    private function generateId(): string
    {
        $timestamp = (int)(microtime(true) * 1000);
        $random = bin2hex(random_bytes(4));
        return "user_{$timestamp}_{$random}";
    }

    /**
     * Map database row to User object
     *
     * @param array $row Database row
     * @return User User object
     */
    private function mapRowToUser(array $row): User
    {
        return new User(
            id: $row['id'],
            email: $row['email'],
            name: $row['name'],
            createdAt: new DateTime($row['created_at'])
        );
    }
}
