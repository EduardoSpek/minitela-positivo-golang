package main

import (
	"fmt"
	"golang.org/x/sys/windows/registry"
)

const (
	runKeyPath  = `Software\Microsoft\Windows\CurrentVersion\Run`
	appRunValue = "MinitelaGo"
)

// IsAutoStartEnabled checks whether the app is registered to start with Windows.
func IsAutoStartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(appRunValue)
	return err == nil
}

// SetAutoStart registers the app to start with Windows.
// exePath is the absolute path to the executable.
func SetAutoStart(exePath, args string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("abrir chave de inicialização: %w", err)
	}
	defer k.Close()
	value := fmt.Sprintf(`"%s"`, exePath)
	if args != "" {
		value += " " + args
	}
	if err := k.SetStringValue(appRunValue, value); err != nil {
		return fmt.Errorf("registrar inicialização: %w", err)
	}
	return nil
}

// ClearAutoStart removes the app from Windows startup.
func ClearAutoStart() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("abrir chave de inicialização: %w", err)
	}
	defer k.Close()
	if err := k.DeleteValue(appRunValue); err != nil {
		return fmt.Errorf("remover inicialização: %w", err)
	}
	return nil
}
