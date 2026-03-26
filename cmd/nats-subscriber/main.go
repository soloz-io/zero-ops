package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	_ "github.com/lib/pq"
	"github.com/soloz-io/zero-ops/internal/agent-core/client"
)

// InfraStatusUpdate represents a Hub Centralised DB status update
type InfraStatusUpdate struct {
	DeploymentID   string `json:"deployment_id"`
	TenantID       string `json:"tenant_id"`
	AgentID        string `json:"agent_id"`
	SpokeClusterID string `json:"spoke_cluster_id"`
	Status         string `json:"status"`         // provisioning, ready, failed
	Phase          string `json:"phase"`          // Running, Idle, Failed
	Replicas       int32  `json:"replicas"`
	Message        string `json:"message"`
	Error          string `json:"error"`
}

func main() {
	log.Println("Starting NATS Status Subscriber...")

	// Initialize database connection to Hub Centralised DB
	hubDBURL := os.Getenv("HUB_DATABASE_URL")
	if hubDBURL == "" {
		log.Fatal("HUB_DATABASE_URL environment variable required")
	}

	db, err := sql.Open("postgres", hubDBURL)
	if err != nil {
		log.Fatalf("Failed to connect to Hub database: %v", err)
	}
	defer db.Close()

	// Initialize AgentRegistry API client
	agentRegistryURL := os.Getenv("AGENTREGISTRY_URL")
	if agentRegistryURL == "" {
		log.Fatal("AGENTREGISTRY_URL environment variable required")
	}

	agentRegistryClient := client.NewAgentRegistryClient(client.Config{
		BaseURL: agentRegistryURL,
		Timeout: 30 * time.Second,
	})

	subscriber := &StatusSubscriber{
		db:                  db,
		agentRegistryClient: agentRegistryClient,
	}

	// Start listening for pg_notify events
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Received shutdown signal, stopping subscriber...")
		cancel()
	}()

	if err := subscriber.Listen(ctx); err != nil {
		log.Fatalf("Subscriber failed: %v", err)
	}

	log.Println("NATS Status Subscriber stopped")
}

// Listen starts listening for pg_notify events from Hub Centralised DB
func (s *StatusSubscriber) Listen(ctx context.Context) error {
	// Create a listener for pg_notify
	listener := pq.NewListener(s.db.Driver().(*pq.Driver).Open, 10*time.Second, time.Minute, func(ev pq.ListenerEventType, err error) {
		if err != nil {
			log.Printf("Listener error: %v", err)
		}
	})
	defer listener.Close()

	// Listen to the agent_infra_status_updates channel
	if err := listener.Listen("agent_infra_status_updates"); err != nil {
		return fmt.Errorf("failed to listen to pg_notify channel: %w", err)
	}

	log.Println("Listening for agent infrastructure status updates...")

	for {
		select {
		case <-ctx.Done():
			return nil
		case notification := <-listener.Notify:
			if notification != nil {
				if err := s.handleStatusUpdate(ctx, notification.Extra); err != nil {
					log.Printf("Failed to handle status update: %v", err)
				}
			}
		case <-time.After(90 * time.Second):
			// Send periodic ping to keep connection alive
			if err := listener.Ping(); err != nil {
				log.Printf("Ping failed: %v", err)
				return err
			}
		}
	}
}

// handleStatusUpdate processes a single status update notification
func (s *StatusSubscriber) handleStatusUpdate(ctx context.Context, payload string) error {
	var update InfraStatusUpdate
	if err := json.Unmarshal([]byte(payload), &update); err != nil {
		return fmt.Errorf("failed to unmarshal status update: %w", err)
	}

	log.Printf("Processing status update for deployment %s: %s -> %s", 
		update.DeploymentID, update.Status, update.Phase)

	// Map infra status to deployment status
	deploymentStatus := mapInfraStatusToDeploymentStatus(update.Status)

	// Update AgentRegistry deployment status via API
	if err := s.updateAgentRegistryDeployment(ctx, update.DeploymentID, deploymentStatus); err != nil {
		return fmt.Errorf("failed to update AgentRegistry deployment: %w", err)
	}

	return nil
}

// mapInfraStatusToDeploymentStatus maps Hub infra status to AgentRegistry deployment status
func mapInfraStatusToDeploymentStatus(infraStatus string) string {
	switch infraStatus {
	case "provisioning":
		return "deploying"
	case "ready":
		return "deployed"
	case "failed":
		return "failed"
	default:
		return "deploying" // default fallback
	}
}

// updateAgentRegistryDeployment updates deployment status via AgentRegistry API
func (s *StatusSubscriber) updateAgentRegistryDeployment(ctx context.Context, deploymentID, status string) error {
	log.Printf("Updating AgentRegistry deployment %s to status: %s", deploymentID, status)
	
	// Update via AgentRegistry API (PATCH /v0/deployments/{id})
	updateReq := &client.DeploymentUpdateRequest{
		Status: status,
	}
	
	err := s.agentRegistryClient.UpdateDeployment(ctx, deploymentID, updateReq)
	if err != nil {
		return fmt.Errorf("failed to update deployment via AgentRegistry API: %w", err)
	}
	
	log.Printf("Successfully updated deployment %s status to: %s", deploymentID, status)
	return nil
}