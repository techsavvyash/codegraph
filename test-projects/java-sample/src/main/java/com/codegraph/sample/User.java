package com.codegraph.sample;

/**
 * User data model
 */
public class User {
    private final String id;
    private final String email;
    private final String name;

    /**
     * Create a new User
     *
     * @param id User ID
     * @param email Email address
     * @param name Full name
     */
    public User(String id, String email, String name) {
        this.id = id;
        this.email = email;
        this.name = name;
    }

    public String getId() {
        return id;
    }

    public String getEmail() {
        return email;
    }

    public String getName() {
        return name;
    }

    @Override
    public String toString() {
        return "User{" +
                "id='" + id + '\'' +
                ", email='" + email + '\'' +
                ", name='" + name + '\'' +
                '}';
    }
}
