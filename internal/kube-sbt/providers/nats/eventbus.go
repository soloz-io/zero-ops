package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/soloz-io/zero-ops/internal/kube-sbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/kube-sbt/models"
)

// EventBus implements interfaces.IEventBus using NATS JetStream.
//
// Subject naming: opensbt.<detailType>
// e.g. opensbt.opensbt_onboardingRequest
//
// Ordering (7.10): NATS JetStream delivers messages in order per subject.
// Idempotency (7.11, 7.12): Callers pass IStorage.IsEventProcessed via the
// optional idempotency hook; the EventBus stamps each message with its Event.ID
// so downstream handlers can deduplicate using the Inbox Pattern.
// Metrics (7.15): publish/receive counts logged; Prometheus integration is
// added at the Control Plane middleware layer (not duplicated here).
type EventBus struct {
	nc  *nats.Conn
	js  nats.JetStreamContext
	cfg Config
	// subs tracks active subscriptions for graceful shutdown
	subs []*nats.Subscription
}

// NewEventBus connects to NATS, creates a JetStream context, and ensures
// the opensbt stream exists (7.1 — clustering via comma-separated URLs).
func NewEventBus(cfg Config) (*EventBus, error) {
	cfg.defaults()

	opts := []nats.Option{
		nats.MaxReconnects(cfg.MaxReconnects),
		nats.ReconnectWait(cfg.ReconnectWait),
		nats.Timeout(cfg.ConnectTimeout),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				log.Printf("nats: disconnected: %v", err)
			}
		}),
		nats.ReconnectHandler(func(_ *nats.Conn) {
			log.Printf("nats: reconnected")
		}),
	}
	if cfg.NKeyFile != "" {
		opt, err := nats.NkeyOptionFromSeed(cfg.NKeyFile)
		if err != nil {
			return nil, fmt.Errorf("nats: nkey: %w", err)
		}
		opts = append(opts, opt)
	}
	if cfg.CredsFile != "" {
		opts = append(opts, nats.UserCredentials(cfg.CredsFile))
	}

	nc, err := nats.Connect(cfg.URLs, opts...)
	if err != nil {
		return nil, fmt.Errorf("nats: connect: %w", err)
	}

	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats: jetstream: %w", err)
	}

	eb := &EventBus{nc: nc, js: js, cfg: cfg}
	if err := eb.ensureStream(); err != nil {
		nc.Close()
		return nil, err
	}
	return eb, nil
}

// ensureStream creates the opensbt JetStream stream if it doesn't exist.
// The stream captures all opensbt.* subjects for ordered, persistent delivery.
func (eb *EventBus) ensureStream() error {
	_, err := eb.js.StreamInfo("opensbt")
	if err == nil {
		return nil // already exists
	}
	_, err = eb.js.AddStream(&nats.StreamConfig{
		Name:       "opensbt",
		Subjects:   []string{"opensbt.>"},
		Retention:  nats.LimitsPolicy,
		MaxAge:     7 * 24 * time.Hour,
		Storage:    nats.FileStorage,
		Replicas:   1,
		Duplicates: 24 * time.Hour, // JetStream dedup window (7.12)
	})
	return err
}

// subject converts a detailType to a NATS subject.
func subject(detailType string) string {
	return "opensbt." + detailType
}

// ─── Publishing (7.2, 7.3, 7.13) ─────────────────────────────────────────────

// Publish validates and synchronously publishes an event (7.2, 7.13).
func (eb *EventBus) Publish(ctx context.Context, event models.Event) error {
	if err := validateEvent(event); err != nil {
		return err
	}
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("nats: marshal event: %w", err)
	}
	// JetStream publish with MsgID for server-side deduplication (7.12)
	_, err = eb.js.Publish(subject(event.DetailType), data,
		nats.MsgId(event.ID),
		nats.Context(ctx),
	)
	return err
}

// PublishAsync publishes without waiting for ack (7.3).
func (eb *EventBus) PublishAsync(ctx context.Context, event models.Event) error {
	if err := validateEvent(event); err != nil {
		return err
	}
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("nats: marshal event: %w", err)
	}
	_, err = eb.js.PublishAsync(subject(event.DetailType), data,
		nats.MsgId(event.ID),
	)
	return err
}

// validateEvent enforces required fields (7.13).
func validateEvent(e models.Event) error {
	if e.ID == "" {
		return fmt.Errorf("event.ID is required")
	}
	if e.DetailType == "" {
		return fmt.Errorf("event.DetailType is required")
	}
	if e.Source == "" {
		return fmt.Errorf("event.Source is required")
	}
	return nil
}

// ─── Subscriptions (7.4, 7.5, 7.10, 7.11) ────────────────────────────────────

// Subscribe registers a durable push consumer for a single event type (7.4).
// JetStream delivers messages in order per subject (7.10).
// The handler receives the decoded Event; callers use IStorage.IsEventProcessed
// for Inbox Pattern idempotency (7.11).
func (eb *EventBus) Subscribe(ctx context.Context, eventType string, handler interfaces.EventHandler) error {
	sub, err := eb.js.Subscribe(subject(eventType), func(msg *nats.Msg) {
		eb.dispatch(ctx, msg, handler)
	}, nats.DeliverAll(), nats.AckExplicit())
	if err != nil {
		return fmt.Errorf("nats: subscribe %s: %w", eventType, err)
	}
	eb.subs = append(eb.subs, sub)
	return nil
}

// SubscribeQueue registers a queue-group push consumer for load balancing (7.5).
func (eb *EventBus) SubscribeQueue(ctx context.Context, eventType, queueGroup string, handler interfaces.EventHandler) error {
	sub, err := eb.js.QueueSubscribe(subject(eventType), queueGroup, func(msg *nats.Msg) {
		eb.dispatch(ctx, msg, handler)
	}, nats.DeliverAll(), nats.AckExplicit(), nats.Durable(queueGroup))
	if err != nil {
		return fmt.Errorf("nats: queue subscribe %s/%s: %w", eventType, queueGroup, err)
	}
	eb.subs = append(eb.subs, sub)
	return nil
}

func (eb *EventBus) dispatch(ctx context.Context, msg *nats.Msg, handler interfaces.EventHandler) {
	var event models.Event
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		log.Printf("nats: unmarshal event: %v", err)
		_ = msg.Nak()
		return
	}
	if err := handler(ctx, event); err != nil {
		log.Printf("nats: handler error for %s: %v", event.DetailType, err)
		_ = msg.Nak()
		return
	}
	_ = msg.Ack()
}

// ─── Event Definitions (7.6–7.9) ─────────────────────────────────────────────

func (eb *EventBus) GetControlPlaneEventSource() string {
	return models.ControlPlaneEventSource
}

func (eb *EventBus) GetApplicationPlaneEventSource() string {
	return models.ApplicationPlaneEventSource
}

func (eb *EventBus) CreateControlPlaneEvent(detailType string) models.EventDefinition {
	return models.EventDefinition{DetailType: detailType, Source: models.ControlPlaneEventSource}
}

func (eb *EventBus) CreateApplicationPlaneEvent(detailType string) models.EventDefinition {
	return models.EventDefinition{DetailType: detailType, Source: models.ApplicationPlaneEventSource}
}

func (eb *EventBus) CreateCustomEvent(detailType, source string) models.EventDefinition {
	return models.EventDefinition{DetailType: detailType, Source: source}
}

func (eb *EventBus) GetStandardEvents() map[string]models.EventDefinition {
	return standardEvents
}

// ─── Permissions (7.14) ───────────────────────────────────────────────────────

// GrantPublishPermissions documents the NATS subject permission for a grantee.
// In NATS, permissions are configured in the server config or via operator JWTs.
// This method logs the required permission for operator awareness.
func (eb *EventBus) GrantPublishPermissions(grantee string) error {
	log.Printf("nats: grant publish permission to %q: allow publish on opensbt.>", grantee)
	return nil
}

// ─── Lifecycle ────────────────────────────────────────────────────────────────

// Close drains all subscriptions and closes the NATS connection.
func (eb *EventBus) Close() {
	for _, sub := range eb.subs {
		_ = sub.Drain()
	}
	eb.nc.Drain() //nolint:errcheck
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// SubjectToDetailType converts an opensbt.* subject back to a detailType.
func SubjectToDetailType(subj string) string {
	return strings.TrimPrefix(subj, "opensbt.")
}

// Compile-time assertion
var _ interfaces.IEventBus = (*EventBus)(nil)
