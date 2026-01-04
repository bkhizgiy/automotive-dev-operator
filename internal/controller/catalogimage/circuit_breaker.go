/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package catalogimage

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/containers/image/v5/types"
	"github.com/go-logr/logr"

	automotivev1alpha1 "github.com/centos-automotive-suite/automotive-dev-operator/api/v1alpha1"
)

// CircuitState represents the state of a circuit breaker
type CircuitState string

const (
	// CircuitClosed indicates the circuit is healthy and requests are allowed
	CircuitClosed CircuitState = "Closed"
	// CircuitOpen indicates the circuit is tripped and requests are blocked
	CircuitOpen CircuitState = "Open"
	// CircuitHalfOpen indicates the circuit is testing if the service recovered
	CircuitHalfOpen CircuitState = "HalfOpen"
)

// Default circuit breaker configuration
const (
	defaultFailureThreshold   = 5
	defaultRecoveryTimeout    = 5 * time.Minute
	defaultHalfOpenMaxRetries = 1
)

// CircuitBreakerConfig contains configuration for the circuit breaker
type CircuitBreakerConfig struct {
	// FailureThreshold is the number of failures before opening the circuit
	FailureThreshold int
	// RecoveryTimeout is how long to wait before trying to recover
	RecoveryTimeout time.Duration
	// HalfOpenMaxRetries is the number of test requests in half-open state
	HalfOpenMaxRetries int
}

// DefaultCircuitBreakerConfig returns the default configuration
func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		FailureThreshold:   defaultFailureThreshold,
		RecoveryTimeout:    defaultRecoveryTimeout,
		HalfOpenMaxRetries: defaultHalfOpenMaxRetries,
	}
}

// registryCircuit tracks the circuit breaker state for a single registry
type registryCircuit struct {
	state           CircuitState
	failures        int
	lastFailureTime time.Time
	halfOpenRetries int
}

// CircuitBreakerRegistry manages circuit breakers for multiple registries
type CircuitBreakerRegistry struct {
	mu       sync.RWMutex
	circuits map[string]*registryCircuit
	config   CircuitBreakerConfig
	log      logr.Logger
}

// NewCircuitBreakerRegistry creates a new circuit breaker registry
func NewCircuitBreakerRegistry(config CircuitBreakerConfig, log logr.Logger) *CircuitBreakerRegistry {
	return &CircuitBreakerRegistry{
		circuits: make(map[string]*registryCircuit),
		config:   config,
		log:      log.WithName("circuit-breaker"),
	}
}

// extractRegistryHost extracts the registry host from a registry URL
func extractRegistryHost(registryURL string) string {
	// Parse the URL to extract just the host
	// Registry URLs are in format: registry.example.com/org/repo:tag
	parsed, err := url.Parse("https://" + registryURL)
	if err != nil {
		// Fallback to simple parsing
		for i, c := range registryURL {
			if c == '/' {
				return registryURL[:i]
			}
		}
		return registryURL
	}
	return parsed.Host
}

// getCircuit returns or creates a circuit for a registry host
func (cbr *CircuitBreakerRegistry) getCircuit(registryHost string) *registryCircuit {
	cbr.mu.Lock()
	defer cbr.mu.Unlock()

	if circuit, exists := cbr.circuits[registryHost]; exists {
		return circuit
	}

	circuit := &registryCircuit{
		state: CircuitClosed,
	}
	cbr.circuits[registryHost] = circuit
	return circuit
}

// CanAttempt checks if a request to the registry should be allowed
func (cbr *CircuitBreakerRegistry) CanAttempt(registryURL string) (bool, CircuitState) {
	registryHost := extractRegistryHost(registryURL)
	circuit := cbr.getCircuit(registryHost)

	cbr.mu.RLock()
	defer cbr.mu.RUnlock()

	switch circuit.state {
	case CircuitClosed:
		return true, CircuitClosed
	case CircuitOpen:
		// Check if enough time has passed to try recovery
		if time.Since(circuit.lastFailureTime) >= cbr.config.RecoveryTimeout {
			return true, CircuitHalfOpen
		}
		return false, CircuitOpen
	case CircuitHalfOpen:
		// Allow limited retries in half-open state
		return circuit.halfOpenRetries < cbr.config.HalfOpenMaxRetries, CircuitHalfOpen
	}
	return true, CircuitClosed
}

// RecordSuccess records a successful request
func (cbr *CircuitBreakerRegistry) RecordSuccess(registryURL string) {
	registryHost := extractRegistryHost(registryURL)

	cbr.mu.Lock()
	defer cbr.mu.Unlock()

	circuit, exists := cbr.circuits[registryHost]
	if !exists {
		return
	}

	// Reset the circuit on success
	if circuit.state == CircuitHalfOpen {
		cbr.log.Info("Circuit recovered", "registry", registryHost)
	}

	circuit.state = CircuitClosed
	circuit.failures = 0
	circuit.halfOpenRetries = 0
}

// RecordFailure records a failed request
func (cbr *CircuitBreakerRegistry) RecordFailure(registryURL string) {
	registryHost := extractRegistryHost(registryURL)

	cbr.mu.Lock()
	defer cbr.mu.Unlock()

	circuit := cbr.circuits[registryHost]
	if circuit == nil {
		circuit = &registryCircuit{state: CircuitClosed}
		cbr.circuits[registryHost] = circuit
	}

	circuit.failures++
	circuit.lastFailureTime = time.Now()

	switch circuit.state {
	case CircuitClosed:
		if circuit.failures >= cbr.config.FailureThreshold {
			cbr.log.Info("Circuit opened due to failures", "registry", registryHost, "failures", circuit.failures)
			circuit.state = CircuitOpen
		}
	case CircuitHalfOpen:
		cbr.log.Info("Circuit reopened after half-open failure", "registry", registryHost)
		circuit.state = CircuitOpen
		circuit.halfOpenRetries = 0
	}
}

// GetState returns the current state for a registry
func (cbr *CircuitBreakerRegistry) GetState(registryURL string) CircuitState {
	registryHost := extractRegistryHost(registryURL)

	cbr.mu.RLock()
	defer cbr.mu.RUnlock()

	if circuit, exists := cbr.circuits[registryHost]; exists {
		return circuit.state
	}
	return CircuitClosed
}

// Reset resets the circuit breaker for a registry
func (cbr *CircuitBreakerRegistry) Reset(registryURL string) {
	registryHost := extractRegistryHost(registryURL)

	cbr.mu.Lock()
	defer cbr.mu.Unlock()

	if circuit, exists := cbr.circuits[registryHost]; exists {
		cbr.log.Info("Circuit manually reset", "registry", registryHost)
		circuit.state = CircuitClosed
		circuit.failures = 0
		circuit.halfOpenRetries = 0
	}
}

// CircuitBreakerError is returned when a request is blocked by the circuit breaker
type CircuitBreakerError struct {
	Registry string
	State    CircuitState
}

func (e *CircuitBreakerError) Error() string {
	return fmt.Sprintf("circuit breaker is %s for registry %s", e.State, e.Registry)
}

// CircuitBreakerRegistryClient wraps a RegistryClient with circuit breaker protection
type CircuitBreakerRegistryClient struct {
	client   RegistryClient
	breakers *CircuitBreakerRegistry
}

// NewCircuitBreakerRegistryClient creates a new circuit breaker wrapped registry client
func NewCircuitBreakerRegistryClient(client RegistryClient, breakers *CircuitBreakerRegistry) *CircuitBreakerRegistryClient {
	return &CircuitBreakerRegistryClient{
		client:   client,
		breakers: breakers,
	}
}

// VerifyImageAccessible checks if the image is accessible, respecting circuit breaker state
func (c *CircuitBreakerRegistryClient) VerifyImageAccessible(ctx context.Context, registryURL string, auth *types.DockerAuthConfig) (bool, error) {
	canAttempt, state := c.breakers.CanAttempt(registryURL)
	if !canAttempt {
		return false, &CircuitBreakerError{
			Registry: extractRegistryHost(registryURL),
			State:    state,
		}
	}

	accessible, err := c.client.VerifyImageAccessible(ctx, registryURL, auth)
	if err != nil {
		c.breakers.RecordFailure(registryURL)
		return false, err
	}

	c.breakers.RecordSuccess(registryURL)
	return accessible, nil
}

// GetImageMetadata retrieves metadata, respecting circuit breaker state
func (c *CircuitBreakerRegistryClient) GetImageMetadata(ctx context.Context, registryURL string, auth *types.DockerAuthConfig) (*automotivev1alpha1.RegistryMetadata, error) {
	canAttempt, state := c.breakers.CanAttempt(registryURL)
	if !canAttempt {
		return nil, &CircuitBreakerError{
			Registry: extractRegistryHost(registryURL),
			State:    state,
		}
	}

	metadata, err := c.client.GetImageMetadata(ctx, registryURL, auth)
	if err != nil {
		c.breakers.RecordFailure(registryURL)
		return nil, err
	}

	c.breakers.RecordSuccess(registryURL)
	return metadata, nil
}

// VerifyDigest verifies the digest, respecting circuit breaker state
func (c *CircuitBreakerRegistryClient) VerifyDigest(ctx context.Context, registryURL string, expectedDigest string, auth *types.DockerAuthConfig) (bool, string, error) {
	canAttempt, state := c.breakers.CanAttempt(registryURL)
	if !canAttempt {
		return false, "", &CircuitBreakerError{
			Registry: extractRegistryHost(registryURL),
			State:    state,
		}
	}

	match, digest, err := c.client.VerifyDigest(ctx, registryURL, expectedDigest, auth)
	if err != nil {
		c.breakers.RecordFailure(registryURL)
		return false, "", err
	}

	c.breakers.RecordSuccess(registryURL)
	return match, digest, nil
}
