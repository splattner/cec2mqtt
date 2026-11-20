package main

import (
	"log/slog"
	"slices"
	"sync"
)

// closer is implemented by services which have to be stopped on shutdown.
type closer interface {
	Close()
}

type Container struct {
	mux      sync.RWMutex
	services map[string]interface{}
	// order holds the service names in registration order.
	order []string
}

func NewContainer() *Container {
	return &Container{
		services: make(map[string]interface{}),
	}
}

func (container *Container) Register(name string, service interface{}) {
	logTrace("Registering new service into container", "name", name)

	container.mux.Lock()
	defer container.mux.Unlock()
	if _, ok := container.services[name]; !ok {
		container.order = append(container.order, name)
	}
	container.services[name] = service
}

func (container *Container) Unregister(name string) {
	logTrace("Unregistering new service from container", "name", name)
	container.mux.Lock()
	defer container.mux.Unlock()
	delete(container.services, name)
	container.order = slices.DeleteFunc(container.order, func(n string) bool { return n == name })
}

func (container *Container) Get(name string) interface{} {
	logTrace("Getting service from container", "name", name)
	container.mux.RLock()
	defer container.mux.RUnlock()
	service, ok := container.services[name]
	if !ok {
		slog.Debug("Service not available in container", "name", name)

		return nil
	}

	return service
}

// Close closes all services which implement closer, in reverse registration
// order. Services are registered after the services they depend on, so for
// example the bridges stop before the MQTT connection is closed.
func (container *Container) Close() {
	container.mux.RLock()
	var closers []string
	for _, name := range slices.Backward(container.order) {
		if _, ok := container.services[name].(closer); ok {
			closers = append(closers, name)
		}
	}
	container.mux.RUnlock()

	for _, name := range closers {
		slog.Debug("Closing service", "name", name)
		container.Get(name).(closer).Close()
	}
}
