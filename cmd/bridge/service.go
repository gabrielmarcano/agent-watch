package main

// ServiceInfo holds runtime service status information.
type ServiceInfo struct {
	Running bool
	PID     int
	Message string
}

// ServiceManager defines the platform-specific service lifecycle operations.
type ServiceManager interface {
	Start(binary, configPath, socketPath, stateDir, logPath string) error
	Stop() error
	Status() (ServiceInfo, error)
	RenderTemplate(binary, configPath, socketPath, stateDir, logPath string) (string, error)
}
