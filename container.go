package main

import (
	"log/slog"
	"sync"
)

type Container struct {
	mux      sync.RWMutex
	services map[string]interface{}
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
	container.services[name] = service
}

func (container *Container) Unregister(name string) {
	logTrace("Unregistering new service from container", "name", name)
	container.mux.Lock()
	defer container.mux.Unlock()
	delete(container.services, name)
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
