// github.com/withObsrvr/pluginapi/pluginapi.go
package pluginapi

import (
	"context"
	"time"
)

// PluginType defines the category of a plugin.
type PluginType int

const (
	SourcePlugin PluginType = iota
	ProcessorPlugin
	ConsumerPlugin
	BufferPlugin
)

// Plugin is the basic interface that every plugin must implement.
type Plugin interface {
	// Name returns the plugin's unique name.
	Name() string
	// Version returns the plugin's version.
	Version() string
	// Type returns the category of the plugin.
	Type() PluginType
	// Initialize configures the plugin with a map of settings.
	Initialize(config map[string]interface{}) error
}

// Source is an extension of Plugin that ingests data.
type Source interface {
	Plugin
	// Start begins data ingestion.
	Start(ctx context.Context) error
	// Stop halts ingestion.
	Stop() error
	// Subscribe allows downstream processors to be added.
	Subscribe(proc Processor)
}

// Processor is an extension of Plugin that transforms data.
type Processor interface {
	Plugin
	// Process transforms a message.
	Process(ctx context.Context, msg Message) error
}

// Consumer is an extension of Plugin that consumes data.
type Consumer interface {
	Plugin
	// Process handles a message (e.g. storing it).
	Process(ctx context.Context, msg Message) error
	// Close cleans up any resources used by the consumer.
	Close() error
}

// Buffer interface for plugins that provide buffering between components
type Buffer interface {
	Plugin
	// Write writes a message to the buffer.
	Write(ctx context.Context, msg Message) error
	// Read reads a message from the buffer.
	Read(ctx context.Context) ([]Message, error)
	// Acknowledge acknowledges a message.
	Acknowledge(ctx context.Context, msgIDs []string) error
	// Close closes the buffer.
	Close() error
}

// Message is the unified data structure that flows between plugins.
type Message struct {
	// Payload holds the primary data (often as []byte).
	Payload interface{}
	// Metadata is an optional map with additional info.
	Metadata map[string]interface{}
	// Timestamp indicates when the message was created.
	Timestamp time.Time
}

// ConsumerRegistry is an interface for processors that can register consumers
type ConsumerRegistry interface {
	RegisterConsumer(consumer Consumer)
}
