package config

import (
	"os"
	"path/filepath"
)

func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "tslink"), nil
}

func RegistryPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "registry.json"), nil
}

func PIDPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tslink.pid"), nil
}

func NodesDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "nodes"), nil
}

func AuthKeyPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "authkey"), nil
}

func APIKeyPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "apikey"), nil
}

func LogDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "logs"), nil
}

func EnsureDir() error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	logDir, err := LogDir()
	if err != nil {
		return err
	}
	nodesDir, err := NodesDir()
	if err != nil {
		return err
	}
	for _, d := range []string{dir, logDir, nodesDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}
