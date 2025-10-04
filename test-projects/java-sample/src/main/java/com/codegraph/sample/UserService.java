package com.codegraph.sample;

import java.util.ArrayList;
import java.util.List;
import java.util.Optional;
import java.util.UUID;

/**
 * Service for managing user operations
 */
public class UserService {
    private final DatabaseConnection db;
    private final Logger logger;

    /**
     * Create a new UserService
     *
     * @param db Database connection instance
     * @param logger Logger instance
     */
    public UserService(DatabaseConnection db, Logger logger) {
        this.db = db;
        this.logger = logger;
    }

    /**
     * Create a new user
     *
     * @param email User's email address
     * @param name User's full name
     * @return The created user
     * @throws IllegalArgumentException If email or name is invalid
     */
    public User createUser(String email, String name) {
        logger.info("Creating user: " + email);

        if (email == null || email.isEmpty() || !email.contains("@")) {
            throw new IllegalArgumentException("Invalid email address");
        }

        if (name == null || name.length() < 2) {
            throw new IllegalArgumentException("Invalid name");
        }

        User user = new User(generateId(), email, name);

        db.execute(
            "INSERT INTO users (id, email, name, created_at) VALUES (?, ?, ?, NOW())",
            user.getId(), user.getEmail(), user.getName()
        );

        logger.info("User created: " + user.getId());
        return user;
    }

    /**
     * Get user by ID
     *
     * @param userId User ID
     * @return User if found, empty otherwise
     */
    public Optional<User> getUserById(String userId) {
        logger.debug("Fetching user: " + userId);

        List<User> results = db.query(
            "SELECT * FROM users WHERE id = ?",
            userId
        );

        return results.isEmpty() ? Optional.empty() : Optional.of(results.get(0));
    }

    /**
     * Update user information
     *
     * @param userId User ID
     * @param email New email address (optional)
     * @param name New name (optional)
     * @return Updated user
     * @throws IllegalStateException If user not found
     */
    public User updateUser(String userId, String email, String name) {
        logger.info("Updating user: " + userId);

        List<String> fields = new ArrayList<>();
        List<Object> values = new ArrayList<>();

        if (email != null) {
            fields.add("email = ?");
            values.add(email);
        }

        if (name != null) {
            fields.add("name = ?");
            values.add(name);
        }

        if (fields.isEmpty()) {
            throw new IllegalArgumentException("No fields to update");
        }

        values.add(userId);

        db.execute(
            "UPDATE users SET " + String.join(", ", fields) + " WHERE id = ?",
            values.toArray()
        );

        return getUserById(userId)
            .orElseThrow(() -> new IllegalStateException("User not found: " + userId));
    }

    /**
     * Delete a user
     *
     * @param userId User ID to delete
     */
    public void deleteUser(String userId) {
        logger.info("Deleting user: " + userId);
        db.execute("DELETE FROM users WHERE id = ?", userId);
    }

    /**
     * List all users with pagination
     *
     * @param limit Maximum number of users to return
     * @param offset Number of users to skip
     * @return List of users
     */
    public List<User> listUsers(int limit, int offset) {
        logger.debug("Listing users: limit=" + limit + ", offset=" + offset);

        return db.query(
            "SELECT * FROM users ORDER BY created_at DESC LIMIT ? OFFSET ?",
            limit, offset
        );
    }

    /**
     * Search users by email or name
     *
     * @param query Search query
     * @return List of matching users
     */
    public List<User> searchUsers(String query) {
        logger.debug("Searching users: " + query);

        String searchPattern = "%" + query + "%";
        return db.query(
            "SELECT * FROM users WHERE email LIKE ? OR name LIKE ?",
            searchPattern, searchPattern
        );
    }

    /**
     * Generate a unique user ID
     *
     * @return User ID
     */
    private String generateId() {
        return "user_" + UUID.randomUUID().toString().replace("-", "");
    }
}
