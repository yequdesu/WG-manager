package api

import "os"

var cachedScript string

func ReadClientScript(path string) (string, error) {
	if cachedScript != "" {
		return cachedScript, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	cachedScript = string(data)
	return cachedScript, nil
}
