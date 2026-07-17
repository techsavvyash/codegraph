package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

// serverCmd starts the API server
var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the API server",
	Long:  "Start the REST API server for querying the code graph",
	RunE: func(cmd *cobra.Command, args []string) error {
		port, _ := cmd.Flags().GetInt("port")

		fmt.Printf("Starting API server on port %d...\n", port)
		fmt.Println("API server functionality not yet implemented")

		// Set up signal handling for graceful shutdown
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Handle shutdown signals
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

		go func() {
			<-sigChan
			fmt.Println("\nShutting down server...")
			cancel()
		}()

		// Wait for shutdown signal
		<-ctx.Done()
		return nil
	},
}

func init() {
	rootCmd.AddCommand(serverCmd)

	// Server flags
	serverCmd.Flags().IntP("port", "p", 8080, "Server port")
}
