//go:build !darwin && !linux

package main

import "errors"

type otherServiceManager struct{}

func newServiceManager() ServiceManager {
	return &otherServiceManager{}
}

func (m *otherServiceManager) RenderTemplate(binary, configPath, socketPath, stateDir, logPath string) (string, error) {
	return "", errors.New("unsupported platform")
}

func (m *otherServiceManager) Start(binary, configPath, socketPath, stateDir, logPath string) error {
	return errors.New("unsupported platform")
}

func (m *otherServiceManager) Stop() error {
	return errors.New("unsupported platform")
}

func (m *otherServiceManager) Status() (ServiceInfo, error) {
	return ServiceInfo{}, errors.New("unsupported platform")
}
